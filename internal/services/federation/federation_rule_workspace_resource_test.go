// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// Federation rule workspace acceptance tests require an org:admin OAuth
// bearer token (ANTHROPIC_AUTH_TOKEN): the federation endpoints reject API
// keys outright. No test org exists yet for org-level writes (the same
// blocker as #58) and CI has no durable org:admin token, so these run locally
// only — gated on acctest.PreCheckOAuth, which skips rather than fails when
// the token is absent.
//
// A federation rule already enables one workspace at create time (its
// workspace_id). This resource manages the *extra* workspaces a rule should
// also be usable from, so the test binds the rule to some other workspace in
// the org at creation, then uses anthropic_federation_rule_workspace to
// additionally enable it for acctest.TerraformTestsWorkspaceID — exercising
// the resource against a workspace distinct from the rule's own binding.

// newTestOAuthClient builds an SDK client authenticated with the org:admin
// OAuth bearer token, used for out-of-band fixture setup/teardown and for
// CheckDestroy.
func newTestOAuthClient() anthropic.Client {
	return anthropic.NewClient(option.WithAuthToken(os.Getenv("ANTHROPIC_AUTH_TOKEN")))
}

// testFixtureRSAJWK is the well-known public RSA JWK from RFC 7517 Appendix
// A.1. It only needs to be structurally valid: this test never performs a
// real token exchange, so the key never has to verify a real signature.
var testFixtureRSAJWK = map[string]any{
	"kty": "RSA",
	"n":   "0vx7agoebGcQSuuPiLJXZptN9nndrQmbXEps2aiAFbWhM78LhWx4cbbfAAtVT86zwu1RK7aPFFxuhDR1L6tSoc_BJECPebWKRXjBZCiFV4n3oknjhMstn64tZ_2W-5JsGY4Hc5n9yBXArwl93lqt7_RN5w6Cf0h4QyQ5v-65YGjQR0_FDW2QvzqY368QQMicAtaSqzs8KJZgnYb9c7d0zgdAZHzu6qMQvRL5hajrn1n91CbOpbISD08qNLyrdkt-bFTWhAI4vMQFh6WeZu0fM4lFd2NcRwr3XPksINHaQ-G_xBniIqbw0Ls1jF44-csFCur-kEgU8awapJzKnqDKgw",
	"e":   "AQAB",
	"alg": "RS256",
	"kid": "tf-acc-test-key",
}

// federationRuleWorkspaceTestFixtures holds the out-of-band issuer, service
// account and federation rule a federation_rule_workspace acceptance test
// exercises.
type federationRuleWorkspaceTestFixtures struct {
	IssuerID           string
	ServiceAccountID   string
	FederationRuleID   string
	RuleOwnWorkspaceID string
}

// findOtherWorkspaceID returns the ID of some non-archived workspace in the
// org other than exclude. The federation rule this test creates is bound to
// it at creation time, so the resource under test can enable a genuinely
// *different* workspace (acctest.TerraformTestsWorkspaceID) without
// duplicating the rule's own create-time binding.
func findOtherWorkspaceID(t *testing.T, client anthropic.Client, exclude string) string {
	t.Helper()
	ctx := context.Background()
	pager := client.Beta.Organization.Workspaces.ListAutoPaging(ctx, anthropic.BetaOrganizationWorkspaceListParams{})
	for pager.Next() {
		ws := pager.Current()
		// Skip archived workspaces; the federation rule creation will fail if bound to an archived workspace.
		if ws.ID != exclude && (ws.ArchivedAt == nil || *ws.ArchivedAt == "") {
			return ws.ID
		}
	}
	if err := pager.Err(); err != nil {
		t.Fatalf("failed to list workspaces: %s", err)
	}
	t.Fatal("no active workspace other than acctest.TerraformTestsWorkspaceID found in the org; this test needs at least two non-archived workspaces")
	return ""
}

// setupFederationRuleWorkspaceTestFixtures creates the federation issuer,
// service account and federation rule a test's
// anthropic_federation_rule_workspace targets, and registers a t.Cleanup to
// archive them afterwards.
//
// Cleanup ordering matters: Terraform's automatic post-Steps destroy removes
// the anthropic_federation_rule_workspace enablement first (before any
// t.Cleanup func runs). Within this cleanup, the rule itself must be archived
// before the issuer and service account it references — archiving either of
// those while a live rule still targets them is rejected by the API with a
// 400.
func setupFederationRuleWorkspaceTestFixtures(t *testing.T) federationRuleWorkspaceTestFixtures {
	t.Helper()
	acctest.PreCheckOAuth(t)

	client := newTestOAuthClient()
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	otherWorkspaceID := findOtherWorkspaceID(t, client, acctest.TerraformTestsWorkspaceID)

	issuer, err := client.Beta.Organization.Federation.Issuers.New(ctx, anthropic.BetaOrganizationFederationIssuerNewParams{
		IssuerURL: fmt.Sprintf("https://tf-acc-test-frw-%s.example.com", suffix),
		Name:      fmt.Sprintf("tf-acc-issuer-frw-%s", suffix),
		JWKS: anthropic.BetaOrganizationFederationIssuerNewParamsJWKSUnion{
			OfInline: &anthropic.BetaJWKSInlineParam{
				Keys: []map[string]any{testFixtureRSAJWK},
			},
		},
	})
	if err != nil {
		t.Fatalf("failed to create test federation issuer: %s", err)
	}

	account, err := client.Beta.Organization.ServiceAccounts.New(ctx, anthropic.BetaOrganizationServiceAccountNewParams{
		Name: fmt.Sprintf("tf-acc-svc-frw-%s", suffix),
	})
	if err != nil {
		t.Fatalf("failed to create test service account: %s", err)
	}

	rule, err := client.Beta.Organization.Federation.Rules.New(ctx, anthropic.BetaOrganizationFederationRuleNewParams{
		Name:       fmt.Sprintf("tf-acc-rule-frw-%s", suffix),
		IssuerID:   issuer.ID,
		OAuthScope: "workspace:developer",
		Match: anthropic.BetaFederationRuleMatchParam{
			SubjectPrefix: param.NewOpt(fmt.Sprintf("repo:my-org/tf-acc-frw-%s:*", suffix)),
		},
		Target: anthropic.BetaServiceAccountTargetParam{
			ServiceAccountID: account.ID,
		},
		WorkspaceID: param.NewOpt(otherWorkspaceID),
	})
	if err != nil {
		t.Fatalf("failed to create test federation rule: %s", err)
	}

	t.Cleanup(func() {
		if _, err := client.Beta.Organization.Federation.Rules.Archive(ctx, rule.ID, anthropic.BetaOrganizationFederationRuleArchiveParams{}); err != nil {
			t.Logf("cleanup: failed to archive test federation rule %s: %s", rule.ID, err)
		}
		if _, err := client.Beta.Organization.ServiceAccounts.Archive(ctx, account.ID, anthropic.BetaOrganizationServiceAccountArchiveParams{}); err != nil {
			t.Logf("cleanup: failed to archive test service account %s: %s", account.ID, err)
		}
		if _, err := client.Beta.Organization.Federation.Issuers.Archive(ctx, issuer.ID, anthropic.BetaOrganizationFederationIssuerArchiveParams{}); err != nil {
			t.Logf("cleanup: failed to archive test federation issuer %s: %s", issuer.ID, err)
		}
	})

	return federationRuleWorkspaceTestFixtures{
		IssuerID:           issuer.ID,
		ServiceAccountID:   account.ID,
		FederationRuleID:   rule.ID,
		RuleOwnWorkspaceID: otherWorkspaceID,
	}
}

// testAccCheckFederationRuleWorkspaceRemoved verifies that
// acctest.TerraformTestsWorkspaceID is no longer among the rule's enabled
// workspaces (the API has a hard-delete endpoint for this enablement, unlike
// most other WIF resources).
func testAccCheckFederationRuleWorkspaceRemoved(s *terraform.State) error {
	client := newTestOAuthClient()
	ctx := context.Background()
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "anthropic_federation_rule_workspace" {
			continue
		}
		federationRuleID := rs.Primary.Attributes["federation_rule_id"]
		workspaceID := rs.Primary.Attributes["workspace_id"]

		pager := client.Beta.Organization.Federation.Rules.Workspaces.ListAutoPaging(ctx, federationRuleID, anthropic.BetaOrganizationFederationRuleWorkspaceListParams{})
		for pager.Next() {
			if pager.Current().WorkspaceID == workspaceID {
				return fmt.Errorf("federation rule %s is still enabled for workspace %s after destroy", federationRuleID, workspaceID)
			}
		}
		if err := pager.Err(); err != nil {
			return fmt.Errorf("federation rule %s: unable to verify enablement was removed: %w", federationRuleID, err)
		}
	}
	return nil
}

func testAccFederationRuleWorkspaceConfig(federationRuleID, workspaceID string) string {
	return fmt.Sprintf(`
resource "anthropic_federation_rule_workspace" "test" {
  federation_rule_id = %[1]q
  workspace_id        = %[2]q
}
`, federationRuleID, workspaceID)
}

func TestAccFederationRuleWorkspaceResource_basic(t *testing.T) {
	fixtures := setupFederationRuleWorkspaceTestFixtures(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckOAuth(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckFederationRuleWorkspaceRemoved,
		Steps: []resource.TestStep{
			// Create and Read
			{
				Config: testAccFederationRuleWorkspaceConfig(fixtures.FederationRuleID, acctest.TerraformTestsWorkspaceID),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_federation_rule_workspace.test", "id"),
					resource.TestCheckResourceAttr("anthropic_federation_rule_workspace.test", "federation_rule_id", fixtures.FederationRuleID),
					resource.TestCheckResourceAttr("anthropic_federation_rule_workspace.test", "workspace_id", acctest.TerraformTestsWorkspaceID),
					resource.TestCheckResourceAttrSet("anthropic_federation_rule_workspace.test", "workspace_name"),
					resource.TestCheckResourceAttrSet("anthropic_federation_rule_workspace.test", "created_at"),
				),
			},
			// ImportState round-trip
			{
				ResourceName:      "anthropic_federation_rule_workspace.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}
