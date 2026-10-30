// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package organizationwebhooks_test

import (
	"fmt"
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

const webhookAddr = "anthropic_organization_webhook.test"

func webhookConfig(body string) string {
	return providertest.ProviderConfig(sdktest.AdminKey) + "resource \"anthropic_organization_webhook\" \"test\" {\n" + body + "}\n"
}

const bothEvents = `["user_profile.enforcement_action_opened", "user_profile.enforcement_action_resolved"]`

// webhookState lists the expected state of the endpoint; "" for a string
// means null.
type webhookState struct {
	n                      int
	url, name, description string
	events                 []string
	enabled                bool
	disabledReason, secret string
	secretVersion          int64
	updatedAt              string
}

func (s webhookState) checks() []statecheck.StateCheck {
	str := func(v string) knownvalue.Check {
		if v == "" {
			return knownvalue.Null()
		}
		return knownvalue.StringExact(v)
	}
	events := make([]knownvalue.Check, len(s.events))
	for i, e := range s.events {
		events[i] = knownvalue.StringExact(e)
	}
	var version knownvalue.Check = knownvalue.Null()
	if s.secretVersion != 0 {
		version = knownvalue.Int64Exact(s.secretVersion)
	}
	created := fmt.Sprintf("2026-10-%02dT19:00:00Z", s.n)
	updated := s.updatedAt
	if updated == "" {
		updated = created
	}
	at := func(attr string, c knownvalue.Check) statecheck.StateCheck {
		return statecheck.ExpectKnownValue(webhookAddr, tfjsonpath.New(attr), c)
	}
	return []statecheck.StateCheck{
		at("id", knownvalue.StringExact(webhookID(s.n))),
		at("url", knownvalue.StringExact(s.url)),
		at("enabled_events", knownvalue.SetExact(events)),
		at("name", str(s.name)),
		at("description", str(s.description)),
		at("enabled", knownvalue.Bool(s.enabled)),
		at("disabled_reason", str(s.disabledReason)),
		at("signing_secret", str(s.secret)),
		at("signing_secret_version", version),
		at("created_at", knownvalue.StringExact(created)),
		at("updated_at", knownvalue.StringExact(updated)),
	}
}

func (f *webhooksFake) checkGone(ids ...string) func(*terraform.State) error {
	return func(*terraform.State) error {
		for _, id := range ids {
			if f.endpoint(id) != nil {
				return fmt.Errorf("webhook endpoint %s still exists after destroy", id)
			}
		}
		return nil
	}
}

// TestOrganizationWebhookResource_lifecycle creates an endpoint (the secret
// comes from the create answer), imports it, changes the URL and name in
// place (only the changed fields are sent), disables it, regenerates the
// signing secret without an update, and deletes it on destroy.
func TestOrganizationWebhookResource_lifecycle(t *testing.T) {
	t.Parallel()
	srv := newWebhooksFake(t)
	base := webhookState{n: 1, url: "https://hooks.example.com/a", name: "Enforcement", description: "Primary receiver",
		events: []string{eventOpened, eventClosed}, enabled: true, secret: "whsec_created1"}
	renamed := base
	renamed.url, renamed.name, renamed.updatedAt = "https://hooks.example.com/b", "Enforcement (b)", "2026-10-25T00:00:00Z"
	disabled := renamed
	disabled.enabled = false
	regenerated := disabled
	regenerated.secret, regenerated.secretVersion = "whsec_regenerated1", 1

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkGone(webhookID(1)),
		Steps: []resource.TestStep{
			{
				Config:            webhookConfig("  url = \"https://hooks.example.com/a\"\n  name = \"Enforcement\"\n  description = \"Primary receiver\"\n  enabled_events = " + bothEvents + "\n"),
				ConfigPlanChecks:  expectAction(plancheck.ResourceActionCreate),
				ConfigStateChecks: base.checks(),
			},
			{
				Config:            webhookConfig("  url = \"https://hooks.example.com/a\"\n  name = \"Enforcement\"\n  description = \"Primary receiver\"\n  enabled_events = " + bothEvents + "\n"),
				ResourceName:      webhookAddr,
				ImportState:       true,
				ImportStateVerify: true,
				// No read returns the secret: null after an import.
				ImportStateVerifyIgnore: []string{"signing_secret"},
			},
			{
				Config:            webhookConfig("  url = \"https://hooks.example.com/b\"\n  name = \"Enforcement (b)\"\n  description = \"Primary receiver\"\n  enabled_events = " + bothEvents + "\n"),
				ConfigPlanChecks:  expectAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: renamed.checks(),
			},
			{
				Config:            webhookConfig("  url = \"https://hooks.example.com/b\"\n  name = \"Enforcement (b)\"\n  description = \"Primary receiver\"\n  enabled_events = " + bothEvents + "\n  enabled = false\n"),
				ConfigPlanChecks:  expectAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: disabled.checks(),
			},
			{
				Config:            webhookConfig("  url = \"https://hooks.example.com/b\"\n  name = \"Enforcement (b)\"\n  description = \"Primary receiver\"\n  enabled_events = " + bothEvents + "\n  enabled = false\n  signing_secret_version = 1\n"),
				ConfigPlanChecks:  expectAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: regenerated.checks(),
			},
		},
	})

	srv.AssertJSONBodies(routeCreateWebhook,
		`{"url":"https://hooks.example.com/a","enabled_events":["user_profile.enforcement_action_opened","user_profile.enforcement_action_resolved"],"name":"Enforcement","description":"Primary receiver"}`)
	srv.AssertJSONBodies(routeUpdateWebhook,
		`{"url":"https://hooks.example.com/b","name":"Enforcement (b)"}`,
		`{"state":"disabled"}`)
	srv.AssertCalled(routeRegenSecret, 1)
	srv.AssertCalled(routeDeleteWebhook, 1)
	srv.assertRequests(t)
}

// TestOrganizationWebhookResource_createDisabled: the create body has no
// state, so enabled = false takes a second call; a creation without name or
// description sends neither and keeps them null.
func TestOrganizationWebhookResource_createDisabled(t *testing.T) {
	t.Parallel()
	srv := newWebhooksFake(t)
	want := webhookState{n: 1, url: "https://hooks.example.com/off", events: []string{eventClosed}, secret: "whsec_created1", updatedAt: "2026-10-25T00:00:00Z"}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkGone(webhookID(1)),
		Steps: []resource.TestStep{{
			Config:            webhookConfig("  url = \"https://hooks.example.com/off\"\n  enabled_events = [\"" + eventClosed + "\"]\n  enabled = false\n"),
			ConfigPlanChecks:  expectAction(plancheck.ResourceActionCreate),
			ConfigStateChecks: want.checks(),
		}},
	})
	srv.AssertJSONBodies(routeCreateWebhook, `{"url":"https://hooks.example.com/off","enabled_events":["user_profile.enforcement_action_resolved"]}`)
	srv.AssertJSONBodies(routeUpdateWebhook, `{"state":"disabled"}`)
}

// TestOrganizationWebhookResource_disabledByAnthropic: an endpoint Anthropic
// disabled shows its reason on refresh, and the next apply turns it back on.
func TestOrganizationWebhookResource_disabledByAnthropic(t *testing.T) {
	t.Parallel()
	srv := newWebhooksFake(t)
	config := webhookConfig("  url = \"https://hooks.example.com/a\"\n  enabled_events = [\"" + eventOpened + "\"]\n")
	on := webhookState{n: 1, url: "https://hooks.example.com/a", events: []string{eventOpened}, enabled: true, secret: "whsec_created1"}
	back := on
	back.updatedAt = "2026-10-25T00:00:00Z"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{Config: config, ConfigPlanChecks: expectAction(plancheck.ResourceActionCreate), ConfigStateChecks: on.checks()},
			{
				PreConfig:         func() { srv.disable(webhookID(1), "endpoint_unreachable") },
				Config:            config,
				ConfigPlanChecks:  expectAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: back.checks(),
			},
		},
	})
	srv.AssertJSONBodies(routeUpdateWebhook, `{"state":"enabled"}`)
}

// TestOrganizationWebhookResource_removedOutsideTerraform: an endpoint
// deleted behind Terraform's back leaves the state on refresh, and the next
// apply creates it again.
func TestOrganizationWebhookResource_removedOutsideTerraform(t *testing.T) {
	t.Parallel()
	srv := newWebhooksFake(t)
	config := webhookConfig("  url = \"https://hooks.example.com/a\"\n  enabled_events = [\"" + eventOpened + "\"]\n")
	again := webhookState{n: 2, url: "https://hooks.example.com/a", events: []string{eventOpened}, enabled: true, secret: "whsec_created2"}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkGone(webhookID(2)),
		Steps: []resource.TestStep{
			{Config: config, ConfigPlanChecks: expectAction(plancheck.ResourceActionCreate)},
			{
				PreConfig:         func() { srv.drop(webhookID(1)) },
				Config:            config,
				ConfigPlanChecks:  expectAction(plancheck.ResourceActionCreate),
				ConfigStateChecks: again.checks(),
			},
		},
	})
	srv.AssertCalled(routeCreateWebhook, 2)
}

// TestOrganizationWebhookResource_deleteNotFound: an endpoint already gone
// (404) when Terraform deletes it is not an error.
func TestOrganizationWebhookResource_deleteNotFound(t *testing.T) {
	t.Parallel()
	srv := newWebhooksFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{
				Config:           webhookConfig("  url = \"https://hooks.example.com/a\"\n  enabled_events = [\"" + eventOpened + "\"]\n"),
				ConfigPlanChecks: expectAction(plancheck.ResourceActionCreate),
			},
			{
				PreConfig:        func() { srv.FailNext(routeDeleteWebhook, http.StatusNotFound, 1) },
				Config:           providertest.ProviderConfig(sdktest.AdminKey),
				ConfigPlanChecks: expectAction(plancheck.ResourceActionDestroy),
			},
		},
	})
	srv.AssertCalled(routeDeleteWebhook, 1)
}

// TestOrganizationWebhookResource_regenerationFailure: when the update lands
// but the regeneration fails, the update is kept and the old secret and
// version stay, so the next plan asks for the regeneration again.
func TestOrganizationWebhookResource_regenerationFailure(t *testing.T) {
	t.Parallel()
	srv := newWebhooksFake(t)
	config := func(name string, version int) string {
		return webhookConfig(fmt.Sprintf("  url = \"https://hooks.example.com/a\"\n  name = %q\n  enabled_events = [\"%s\"]\n  signing_secret_version = %d\n", name, eventOpened, version))
	}
	kept := webhookState{n: 1, url: "https://hooks.example.com/a", name: "Renamed", events: []string{eventOpened}, enabled: true,
		secret: "whsec_created1", secretVersion: 1, updatedAt: "2026-10-25T00:00:00Z"}
	fresh := kept
	fresh.secret, fresh.secretVersion = "whsec_regenerated1", 2

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{Config: config("First", 1), ConfigPlanChecks: expectAction(plancheck.ResourceActionCreate)},
			{
				PreConfig:   func() { srv.FailNext(routeRegenSecret, http.StatusForbidden, 1) },
				Config:      config("Renamed", 2),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+regenerate\s+the\s+organization\s+webhook\s+signing\s+secret`),
			},
			{
				Config:            config("Renamed", 1),
				ConfigPlanChecks:  expectAction(plancheck.ResourceActionNoop),
				ConfigStateChecks: kept.checks(),
			},
			{
				Config:            config("Renamed", 2),
				ConfigPlanChecks:  expectAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: fresh.checks(),
			},
		},
	})
	srv.AssertJSONBodies(routeUpdateWebhook, `{"name":"Renamed"}`)
	srv.AssertCalled(routeRegenSecret, 2)
}

// TestOrganizationWebhookResource_apiErrors: an API error on create (a URL
// another enabled endpoint uses), refresh, update or delete is an error
// diagnostic naming the operation.
func TestOrganizationWebhookResource_apiErrors(t *testing.T) {
	t.Parallel()
	srv := newWebhooksFake(t)
	srv.put(endpointJSON(1, "https://hooks.example.com/taken", "", "", eventOpened))
	// "Two enabled organization webhook endpoints in your organization can't
	// share a URL": the API's 409 to the create, and to the update below.
	srv.FailNext(routeCreateWebhook, http.StatusConflict, 1)
	config := func(url string) string {
		return webhookConfig("  url = \"" + url + "\"\n  enabled_events = [\"" + eventOpened + "\"]\n")
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkGone(webhookID(2)),
		Steps: []resource.TestStep{
			{
				Config:      config("https://hooks.example.com/taken"),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+create\s+organization\s+webhook:\s+API\s+error\s+\(409\s+conflict_error\)`),
			},
			{Config: config("https://hooks.example.com/mine"), ConfigPlanChecks: expectAction(plancheck.ResourceActionCreate)},
			{
				PreConfig:   func() { srv.FailNext(routeGetWebhook, http.StatusForbidden, 1) },
				Config:      config("https://hooks.example.com/mine"),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+read\s+organization\s+webhook:\s+API\s+error\s+\(403\s+permission_error\)`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeUpdateWebhook, http.StatusConflict, 1) },
				Config:      config("https://hooks.example.com/taken"),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+update\s+organization\s+webhook:\s+API\s+error\s+\(409\s+conflict_error\)`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeDeleteWebhook, http.StatusForbidden, 1) },
				Config:      providertest.ProviderConfig(sdktest.AdminKey),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+delete\s+organization\s+webhook:\s+API\s+error\s+\(403\s+permission_error\)`),
			},
		},
	})
	srv.AssertCalled(routeCreateWebhook, 2)
}

// TestOrganizationWebhookResource_disableAfterCreateFails: when the second
// call that disables a new endpoint fails, the endpoint is saved as created
// (tainted, secret kept) and the next apply replaces it.
func TestOrganizationWebhookResource_disableAfterCreateFails(t *testing.T) {
	t.Parallel()
	srv := newWebhooksFake(t)
	config := webhookConfig("  url = \"https://hooks.example.com/off\"\n  enabled_events = [\"" + eventOpened + "\"]\n  enabled = false\n")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkGone(webhookID(1), webhookID(2)),
		Steps: []resource.TestStep{
			{
				PreConfig:   func() { srv.FailNext(routeUpdateWebhook, http.StatusBadRequest, 1) },
				Config:      config,
				ExpectError: regexp.MustCompile(`Organization\s+webhook\s+` + webhookID(1) + `\s+was\s+created\s+but\s+could\s+not\s+be\s+set\s+to\s+state\s+"disabled"`),
			},
			{
				Config:            config,
				ConfigPlanChecks:  expectAction(plancheck.ResourceActionReplace),
				ConfigStateChecks: webhookState{n: 2, url: "https://hooks.example.com/off", events: []string{eventOpened}, secret: "whsec_created2", updatedAt: "2026-10-25T00:00:00Z"}.checks(),
			},
		},
	})
}

// TestOrganizationWebhookResource_invalidConfig: a non-HTTPS URL, an empty or
// unknown event list, an empty name fail the plan, before any request.
func TestOrganizationWebhookResource_invalidConfig(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"http url":      "  url = \"http://hooks.example.com\"\n  enabled_events = [\"" + eventOpened + "\"]\n",
		"no event":      "  url = \"https://hooks.example.com\"\n  enabled_events = []\n",
		"unknown event": "  url = \"https://hooks.example.com\"\n  enabled_events = [\"user_profile.deleted\"]\n",
		"empty name":    "  url = \"https://hooks.example.com\"\n  enabled_events = [\"" + eventOpened + "\"]\n  name = \"\"\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newWebhooksFake(t)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
				Steps: []resource.TestStep{{
					Config:      webhookConfig(body),
					ExpectError: regexp.MustCompile(`Invalid\s+Attribute\s+Value`),
				}},
			})
			srv.AssertCalled("", 0)
		})
	}
}
