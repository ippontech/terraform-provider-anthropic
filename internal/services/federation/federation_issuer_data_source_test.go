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
)

// anthropic_federation_issuer acceptance tests require an org:admin OAuth
// bearer token (ANTHROPIC_AUTH_TOKEN): the federation endpoints reject API
// keys outright. No test org exists yet for org-level writes (the same
// blocker as #58) and CI has no durable org:admin token, so this runs locally
// only — gated on acctest.PreCheckOAuth, which skips rather than fails when
// the token is absent.
//
// This is a smoke test only (see CLAUDE.md's admin-data-source testing
// pattern): it asserts attribute presence/consistency rather than golden
// values, since the fixture issuer is created directly through the SDK, not
// through a sibling anthropic_federation_issuer resource (not present on this
// branch). The deterministic mapping coverage — every jwks type, poll_status,
// 404 — lives in federation_issuer_data_source_internal_test.go.

func newTestFederationOAuthClient() anthropic.Client {
	return anthropic.NewClient(option.WithAuthToken(os.Getenv("ANTHROPIC_AUTH_TOKEN")))
}

// setupFederationIssuerDataSourceFixture creates a federation issuer directly
// through the SDK for the data source to read, and registers a t.Cleanup to
// archive it afterwards.
func setupFederationIssuerDataSourceFixture(t *testing.T) *anthropic.BetaFederationIssuer {
	t.Helper()
	acctest.PreCheckOAuth(t)

	client := newTestFederationOAuthClient()
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	issuer, err := client.Beta.Organization.Federation.Issuers.New(ctx, anthropic.BetaOrganizationFederationIssuerNewParams{
		IssuerURL: fmt.Sprintf("https://tf-acc-test-%s.example.com", suffix),
		Name:      fmt.Sprintf("tf-acc-fdis-%s", suffix),
	})
	if err != nil {
		t.Fatalf("failed to create test federation issuer: %s", err)
	}

	t.Cleanup(func() {
		if _, err := client.Beta.Organization.Federation.Issuers.Archive(ctx, issuer.ID, anthropic.BetaOrganizationFederationIssuerArchiveParams{}); err != nil {
			t.Logf("cleanup: failed to archive test federation issuer %s: %s", issuer.ID, err)
		}
	})

	return issuer
}

func testAccFederationIssuerDataSourceConfig(issuerID string) string {
	return fmt.Sprintf(`
data "anthropic_federation_issuer" "test" {
  id = %[1]q
}
`, issuerID)
}

func TestAccFederationIssuerDataSource_basic(t *testing.T) {
	issuer := setupFederationIssuerDataSourceFixture(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckOAuth(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccFederationIssuerDataSourceConfig(issuer.ID),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.anthropic_federation_issuer.test", "id", issuer.ID),
					resource.TestCheckResourceAttr("data.anthropic_federation_issuer.test", "name", issuer.Name),
					resource.TestCheckResourceAttr("data.anthropic_federation_issuer.test", "issuer_url", issuer.IssuerURL),
					resource.TestCheckResourceAttrSet("data.anthropic_federation_issuer.test", "created_at"),
					resource.TestCheckResourceAttrSet("data.anthropic_federation_issuer.test", "jwks.type"),
					resource.TestCheckResourceAttr("data.anthropic_federation_issuer.test", "poll_status.consecutive_failures", "0"),
				),
			},
		},
	})
}
