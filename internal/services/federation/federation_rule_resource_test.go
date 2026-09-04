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
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// Federation rule acceptance tests require an org:admin OAuth bearer token
// (ANTHROPIC_AUTH_TOKEN): the federation endpoints reject API keys outright.
// No test org exists yet for org-level writes (the same blocker as #58) and CI
// has no durable org:admin token, so these run locally only — gated on
// acctest.PreCheckOAuth, which skips rather than fails when the token is
// absent.
//
// The example this resource ships (examples/resources/federation_rule) chains
// anthropic_federation_issuer and anthropic_service_account, which are
// implemented on sibling branches (see #137) and do not exist on this branch.
// To keep this branch self-contained, the issuer and service account this
// test's rule targets are created directly through the SDK in test setup, not
// through those Terraform resources.

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

// federationRuleTestFixtures holds the out-of-band issuer and service account
// a federation rule acceptance test targets.
type federationRuleTestFixtures struct {
	IssuerID         string
	ServiceAccountID string
}

// setupFederationRuleTestFixtures creates the federation issuer and service
// account a test's anthropic_federation_rule targets, and registers a
// t.Cleanup to archive them afterwards.
//
// Cleanup ordering matters: the rule itself is destroyed by the Terraform
// testing framework's automatic post-Steps destroy, which runs before
// t.Cleanup funcs — so by the time this archives the issuer and service
// account, no rule still references them. Archiving out of that order (while
// an active rule still targets them) is rejected by the API with a 400.
func setupFederationRuleTestFixtures(t *testing.T) federationRuleTestFixtures {
	t.Helper()
	acctest.PreCheckOAuth(t)

	client := newTestOAuthClient()
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	issuer, err := client.Beta.Organization.Federation.Issuers.New(ctx, anthropic.BetaOrganizationFederationIssuerNewParams{
		IssuerURL: fmt.Sprintf("https://tf-acc-test-%s.example.com", suffix),
		Name:      fmt.Sprintf("tf-acc-issuer-%s", suffix),
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
		Name: fmt.Sprintf("tf-acc-svc-%s", suffix),
	})
	if err != nil {
		t.Fatalf("failed to create test service account: %s", err)
	}

	t.Cleanup(func() {
		if _, err := client.Beta.Organization.ServiceAccounts.Archive(ctx, account.ID, anthropic.BetaOrganizationServiceAccountArchiveParams{}); err != nil {
			t.Logf("cleanup: failed to archive test service account %s: %s", account.ID, err)
		}
		if _, err := client.Beta.Organization.Federation.Issuers.Archive(ctx, issuer.ID, anthropic.BetaOrganizationFederationIssuerArchiveParams{}); err != nil {
			t.Logf("cleanup: failed to archive test federation issuer %s: %s", issuer.ID, err)
		}
	})

	return federationRuleTestFixtures{IssuerID: issuer.ID, ServiceAccountID: account.ID}
}

// testAccCheckFederationRuleArchivedAndCleanup verifies the rule was archived
// (the API has no hard-delete endpoint, so Delete always archives) rather than
// erroring as "still exists".
func testAccCheckFederationRuleArchivedAndCleanup(s *terraform.State) error {
	client := newTestOAuthClient()
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "anthropic_federation_rule" {
			continue
		}
		rule, err := client.Beta.Organization.Federation.Rules.Get(context.Background(), rs.Primary.ID, anthropic.BetaOrganizationFederationRuleGetParams{})
		if err != nil {
			return fmt.Errorf("federation rule %s: unable to verify archive state: %w", rs.Primary.ID, err)
		}
		if rule.ArchivedAt.IsZero() {
			return fmt.Errorf("federation rule %s was not archived on destroy", rs.Primary.ID)
		}
	}
	return nil
}

func testAccFederationRuleConfig(issuerID, serviceAccountID string, tokenLifetimeSeconds int) string {
	return fmt.Sprintf(`
resource "anthropic_federation_rule" "test" {
  name        = "tf-acc-gha-deploy"
  description = "tf-acc test rule"
  issuer_id   = %[1]q

  match = {
    subject_prefix = "repo:my-org/my-repo:*"
  }

  target = {
    service_account_id = %[2]q
  }

  oauth_scope            = "workspace:developer"
  workspace_id           = %[3]q
  token_lifetime_seconds = %[4]d
}
`, issuerID, serviceAccountID, acctest.TerraformTestsWorkspaceID, tokenLifetimeSeconds)
}

func TestAccFederationRuleResource_basic(t *testing.T) {
	fixtures := setupFederationRuleTestFixtures(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckOAuth(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckFederationRuleArchivedAndCleanup,
		Steps: []resource.TestStep{
			// Create and Read
			{
				Config: testAccFederationRuleConfig(fixtures.IssuerID, fixtures.ServiceAccountID, 3600),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_federation_rule.test", "id"),
					resource.TestCheckResourceAttr("anthropic_federation_rule.test", "name", "tf-acc-gha-deploy"),
					resource.TestCheckResourceAttr("anthropic_federation_rule.test", "issuer_id", fixtures.IssuerID),
					resource.TestCheckResourceAttr("anthropic_federation_rule.test", "target.service_account_id", fixtures.ServiceAccountID),
					resource.TestCheckResourceAttr("anthropic_federation_rule.test", "oauth_scope", "workspace:developer"),
					resource.TestCheckResourceAttr("anthropic_federation_rule.test", "workspace_id", acctest.TerraformTestsWorkspaceID),
					resource.TestCheckResourceAttr("anthropic_federation_rule.test", "token_lifetime_seconds", "3600"),
					resource.TestCheckResourceAttr("anthropic_federation_rule.test", "match.subject_prefix", "repo:my-org/my-repo:*"),
					resource.TestCheckResourceAttrSet("anthropic_federation_rule.test", "issuer_name"),
					resource.TestCheckResourceAttrSet("anthropic_federation_rule.test", "created_at"),
				),
			},
			// Update: token_lifetime_seconds
			{
				Config: testAccFederationRuleConfig(fixtures.IssuerID, fixtures.ServiceAccountID, 7200),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("anthropic_federation_rule.test", "token_lifetime_seconds", "7200"),
				),
			},
			// ImportState round-trip
			{
				ResourceName:      "anthropic_federation_rule.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}
