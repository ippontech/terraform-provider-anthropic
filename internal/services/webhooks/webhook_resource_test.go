// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package webhooks_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	sdkacctest "github.com/hashicorp/terraform-plugin-testing/helper/acctest"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

// Live tests of anthropic_webhook. A webhook endpoint delivers real events to
// its URL, so every endpoint here points at example.com, subscribes to
// vault_credential.refresh_failed (which the test workspaces never emit:
// they hold no mcp_oauth credential) and stays disabled except in the step
// that toggles it. Enabled URLs are unique across the whole organization
// (the API answers 409 otherwise), so each test draws its own URL.

const (
	webhookResourceName = "anthropic_webhook.test"
	webhookBeta         = "managed-agents-2026-04-01"
	rareEvent           = "vault_credential.refresh_failed"
	otherRareEvent      = "memory_store.deleted"
)

var (
	webhookIDPattern     = regexp.MustCompile(`^wep_`)
	signingSecretPattern = regexp.MustCompile(`^whsec_`)
)

// testAccWebhookURL returns an example.com URL no other test run uses.
func testAccWebhookURL() string {
	return "https://example.com/tf-acc-webhook-" + sdkacctest.RandString(16)
}

type webhookConfig struct {
	providerBlock        string
	url                  string
	events               []string
	name                 string
	description          string
	enabled              bool
	signingSecretVersion int64 // 0 leaves the attribute out
}

func (c webhookConfig) String() string {
	events := ""
	for _, e := range c.events {
		events += fmt.Sprintf("%q, ", e)
	}
	cfg := c.providerBlock + fmt.Sprintf(`
resource "anthropic_webhook" "test" {
  url            = %q
  enabled_events = [%s]
  enabled        = %t
`, c.url, events, c.enabled)
	if c.name != "" {
		cfg += fmt.Sprintf("  name        = %q\n", c.name)
	}
	if c.description != "" {
		cfg += fmt.Sprintf("  description = %q\n", c.description)
	}
	if c.signingSecretVersion != 0 {
		cfg += fmt.Sprintf("  signing_secret_version = %d\n", c.signingSecretVersion)
	}
	return cfg + "}\n"
}

// webhookEndpoint is the part of an endpoint the out-of-band checks read.
type webhookEndpoint struct {
	ID    string `json:"id"`
	State string `json:"state"`
	URL   string `json:"url"`
}

// getWebhook reads an endpoint out of band, through the raw route the
// resource uses: no SDK release manages webhook endpoints.
func getWebhook(client *anthropic.Client, id string) (*webhookEndpoint, error) {
	var ep webhookEndpoint
	err := client.Execute(context.Background(), http.MethodGet, "v1/webhooks/"+id, nil, &ep,
		option.WithHeader("anthropic-beta", webhookBeta))
	if err != nil {
		return nil, err
	}
	return &ep, nil
}

func isNotFound(err error) bool {
	var apierr *anthropic.Error
	return errors.As(err, &apierr) && apierr.StatusCode == http.StatusNotFound
}

// testAccCheckWebhookState asserts the endpoint's `state` as the API reports
// it, so `enabled` is checked against the server and not only against state.
func testAccCheckWebhookState(client func() *anthropic.Client, want string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[webhookResourceName]
		if !ok {
			return fmt.Errorf("%s not found in state", webhookResourceName)
		}
		ep, err := getWebhook(client(), rs.Primary.ID)
		if err != nil {
			return fmt.Errorf("reading webhook %s: %w", rs.Primary.ID, err)
		}
		if ep.State != want {
			return fmt.Errorf("webhook %s: state %q, want %q", rs.Primary.ID, ep.State, want)
		}
		return nil
	}
}

func testAccCheckWebhookDestroyed(client func() *anthropic.Client) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		for _, rs := range s.RootModule().Resources {
			if rs.Type != "anthropic_webhook" {
				continue
			}
			_, err := getWebhook(client(), rs.Primary.ID)
			if err == nil {
				return fmt.Errorf("webhook %s still exists", rs.Primary.ID)
			}
			if !isNotFound(err) {
				return fmt.Errorf("reading webhook %s after destroy: want 404, got %w", rs.Primary.ID, err)
			}
		}
		return nil
	}
}

// captureAttr stores an attribute's value, for a later step to compare with.
func captureAttr(name string, into *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[webhookResourceName]
		if !ok {
			return fmt.Errorf("%s not found in state", webhookResourceName)
		}
		*into = rs.Primary.Attributes[name]
		return nil
	}
}

// checkAttrEquals and checkAttrDiffers compare an attribute with a value an
// earlier step captured: an in-place update keeps the ID, a rotation changes
// the secret.
func checkAttrEquals(name string, prev *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		return resource.TestCheckResourceAttr(webhookResourceName, name, *prev)(s)
	}
}

func checkAttrDiffers(name string, prev *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[webhookResourceName]
		if !ok {
			return fmt.Errorf("%s not found in state", webhookResourceName)
		}
		if got := rs.Primary.Attributes[name]; got == *prev {
			return fmt.Errorf("%s did not change", name)
		}
		return nil
	}
}

// Create, import (the signing secret is null afterwards, as no read returns
// it), then the documented way to get a secret back into state: setting
// signing_secret_version regenerates it.
func TestAccWebhookResource_basic(t *testing.T) {
	cfg := webhookConfig{
		url:         testAccWebhookURL(),
		events:      []string{rareEvent},
		name:        "tf-acc-test-webhook",
		description: "Created by the provider's acceptance tests",
	}
	var id, secret string
	// The endpoint leaves the state between two steps: delete it if the test
	// stops there, so no endpoint outlives a failed run.
	t.Cleanup(func() {
		if id != "" {
			_ = acctest.NewAPIKeyClient().Execute(context.Background(), http.MethodDelete, "v1/webhooks/"+id, nil, nil,
				option.WithHeader("anthropic-beta", webhookBeta))
		}
	})

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckWebhookDestroyed(acctest.NewAPIKeyClient),
		Steps: []resource.TestStep{
			{
				Config: cfg.String(),
				Check: resource.ComposeAggregateTestCheckFunc(
					captureAttr("id", &id),
					resource.TestMatchResourceAttr(webhookResourceName, "id", webhookIDPattern),
					resource.TestCheckResourceAttr(webhookResourceName, "url", cfg.url),
					resource.TestCheckResourceAttr(webhookResourceName, "enabled_events.#", "1"),
					resource.TestCheckTypeSetElemAttr(webhookResourceName, "enabled_events.*", rareEvent),
					resource.TestCheckResourceAttr(webhookResourceName, "name", cfg.name),
					resource.TestCheckResourceAttr(webhookResourceName, "description", cfg.description),
					resource.TestCheckResourceAttr(webhookResourceName, "enabled", "false"),
					resource.TestCheckNoResourceAttr(webhookResourceName, "disabled_reason"),
					resource.TestMatchResourceAttr(webhookResourceName, "signing_secret", signingSecretPattern),
					resource.TestCheckResourceAttrSet(webhookResourceName, "created_at"),
					resource.TestCheckResourceAttrSet(webhookResourceName, "updated_at"),
					testAccCheckWebhookState(acctest.NewAPIKeyClient, "disabled"),
					captureAttr("signing_secret", &secret),
				),
			},
			{
				ResourceName:            webhookResourceName,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"signing_secret"},
			},
			{
				// Forget the endpoint without deleting it, so the next step
				// imports it into an empty state.
				Config: cfg.providerBlock + `
removed {
  from = anthropic_webhook.test
  lifecycle {
    destroy = false
  }
}
`,
			},
			{
				// Keep the imported state, whose signing secret is null.
				Config:             cfg.String(),
				ResourceName:       webhookResourceName,
				ImportState:        true,
				ImportStatePersist: true,
				ImportStateIdFunc:  func(*terraform.State) (string, error) { return id, nil },
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					if len(states) != 1 {
						return fmt.Errorf("imported %d instances, want 1", len(states))
					}
					if v := states[0].Attributes["signing_secret"]; v != "" {
						return fmt.Errorf("signing_secret after import: want null, got a value")
					}
					return nil
				},
			},
			{
				Config: func() string { c := cfg; c.signingSecretVersion = 1; return c.String() }(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestMatchResourceAttr(webhookResourceName, "signing_secret", signingSecretPattern),
					checkAttrDiffers("signing_secret", &secret),
					resource.TestCheckResourceAttr(webhookResourceName, "signing_secret_version", "1"),
				),
			},
		},
	})
}

// Every mutable attribute changes in place: same ID, same signing secret.
// The enabled step turns deliveries on, then the last step turns them off.
func TestAccWebhookResource_update(t *testing.T) {
	first := webhookConfig{
		url:    testAccWebhookURL(),
		events: []string{rareEvent},
		name:   "tf-acc-test-webhook",
	}
	second := first
	second.url = testAccWebhookURL()
	second.events = []string{rareEvent, otherRareEvent}
	second.name = "tf-acc-test-webhook-renamed"
	second.description = "Updated by the provider's acceptance tests"
	enabled := second
	enabled.events = []string{rareEvent}
	enabled.enabled = true
	disabledAgain := enabled
	disabledAgain.enabled = false

	var id, secret string
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckWebhookDestroyed(acctest.NewAPIKeyClient),
		Steps: []resource.TestStep{
			{
				Config: first.String(),
				Check: resource.ComposeAggregateTestCheckFunc(
					captureAttr("id", &id),
					captureAttr("signing_secret", &secret),
					resource.TestCheckNoResourceAttr(webhookResourceName, "description"),
				),
			},
			{
				Config: second.String(),
				Check: resource.ComposeAggregateTestCheckFunc(
					checkAttrEquals("id", &id),
					checkAttrEquals("signing_secret", &secret),
					resource.TestCheckResourceAttr(webhookResourceName, "url", second.url),
					resource.TestCheckResourceAttr(webhookResourceName, "enabled_events.#", "2"),
					resource.TestCheckTypeSetElemAttr(webhookResourceName, "enabled_events.*", otherRareEvent),
					resource.TestCheckResourceAttr(webhookResourceName, "name", second.name),
					resource.TestCheckResourceAttr(webhookResourceName, "description", second.description),
					testAccCheckWebhookState(acctest.NewAPIKeyClient, "disabled"),
				),
			},
			{
				Config: enabled.String(),
				Check: resource.ComposeAggregateTestCheckFunc(
					checkAttrEquals("id", &id),
					resource.TestCheckResourceAttr(webhookResourceName, "enabled", "true"),
					resource.TestCheckResourceAttr(webhookResourceName, "enabled_events.#", "1"),
					testAccCheckWebhookState(acctest.NewAPIKeyClient, "enabled"),
				),
			},
			{
				Config: disabledAgain.String(),
				Check: resource.ComposeAggregateTestCheckFunc(
					checkAttrEquals("id", &id),
					resource.TestCheckResourceAttr(webhookResourceName, "enabled", "false"),
					testAccCheckWebhookState(acctest.NewAPIKeyClient, "disabled"),
				),
			},
		},
	})
}

// Changing signing_secret_version regenerates the secret in place. The
// regeneration moves updated_at past the update response; resource.Test's
// empty-plan check after each step catches a resource that does not read it
// back.
func TestAccWebhookResource_rotateSigningSecret(t *testing.T) {
	cfg := webhookConfig{url: testAccWebhookURL(), events: []string{rareEvent}, signingSecretVersion: 1}
	rotated := cfg
	rotated.signingSecretVersion = 2
	rotatedAndRenamed := rotated
	rotatedAndRenamed.signingSecretVersion = 3
	rotatedAndRenamed.name = "tf-acc-test-webhook-rotated"

	var id, secret string
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckWebhookDestroyed(acctest.NewAPIKeyClient),
		Steps: []resource.TestStep{
			{
				Config: cfg.String(),
				Check: resource.ComposeAggregateTestCheckFunc(
					captureAttr("id", &id),
					captureAttr("signing_secret", &secret),
				),
			},
			{
				Config: rotated.String(),
				Check: resource.ComposeAggregateTestCheckFunc(
					checkAttrEquals("id", &id),
					resource.TestMatchResourceAttr(webhookResourceName, "signing_secret", signingSecretPattern),
					checkAttrDiffers("signing_secret", &secret),
					captureAttr("signing_secret", &secret),
				),
			},
			{
				// A rotation combined with another change.
				Config: rotatedAndRenamed.String(),
				Check: resource.ComposeAggregateTestCheckFunc(
					checkAttrEquals("id", &id),
					checkAttrDiffers("signing_secret", &secret),
					resource.TestCheckResourceAttr(webhookResourceName, "name", rotatedAndRenamed.name),
				),
			},
		},
	})
}

// With the provider's workspace_id, a key bound to no single workspace
// manages the endpoint in the named workspace and in no other. The resource
// sends raw requests (client.Execute) rather than typed SDK calls, so this
// checks that the provider's default header reaches them, regeneration
// included. terraform-tests-2 is the target: the multi-workspace key is
// rejected without the header, so any routing slip fails or lands the
// endpoint in terraform-tests, which the check rules out.
func TestAccWebhookResource_multiWorkspaceKeyRoutesToWorkspaceID(t *testing.T) {
	workspaceID := acctest.TerraformTests2WorkspaceID
	providerBlock := fmt.Sprintf("provider \"anthropic\" {\n  api_key      = %q\n  workspace_id = %q\n}\n",
		os.Getenv(acctest.MultiWorkspaceAPIKeyEnv), workspaceID)
	cfg := webhookConfig{
		providerBlock:        providerBlock,
		url:                  testAccWebhookURL(),
		events:               []string{rareEvent},
		name:                 "tf-acc-test-webhook-terraform-tests-2",
		signingSecretVersion: 1,
	}
	rotated := cfg
	rotated.signingSecretVersion = 2
	here := func() *anthropic.Client { return acctest.NewMultiWorkspaceClient(workspaceID) }

	var secret string
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckMultiWorkspace(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckWebhookDestroyed(here),
		Steps: []resource.TestStep{
			{
				Config: cfg.String(),
				Check: resource.ComposeAggregateTestCheckFunc(
					testAccCheckWebhookState(here, "disabled"),
					testAccCheckWebhookNotIn(acctest.TerraformTestsWorkspaceID),
					captureAttr("signing_secret", &secret),
				),
			},
			{
				Config: rotated.String(),
				Check: resource.ComposeAggregateTestCheckFunc(
					checkAttrDiffers("signing_secret", &secret),
					testAccCheckWebhookState(here, "disabled"),
				),
			},
		},
	})
}

// testAccCheckWebhookNotIn asserts the endpoint answers 404 from workspaceID.
func testAccCheckWebhookNotIn(workspaceID string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[webhookResourceName]
		if !ok {
			return fmt.Errorf("%s not found in state", webhookResourceName)
		}
		_, err := getWebhook(acctest.NewMultiWorkspaceClient(workspaceID), rs.Primary.ID)
		if err == nil {
			return fmt.Errorf("webhook %s is also visible from workspace %s: the header did not scope the write", rs.Primary.ID, workspaceID)
		}
		if !isNotFound(err) {
			return fmt.Errorf("reading webhook %s from workspace %s: want 404, got %w", rs.Primary.ID, workspaceID, err)
		}
		return nil
	}
}
