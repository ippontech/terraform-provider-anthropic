// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation_test

import (
	"testing"

	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// TestAccFederationIssuersDataSource is a smoke test only: it targets the live
// org tied to ANTHROPIC_AUTH_TOKEN, whose set of registered issuers is not
// deterministic, so it asserts attribute presence (issuers.#) rather than
// specific values. No test org exists yet for org-level WIF writes (same
// blocker as #58), so this never creates a federation issuer to read back.
func TestAccFederationIssuersDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckOAuth(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccFederationIssuersDataSourceConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.anthropic_federation_issuers.test", "issuers.#"),
				),
			},
		},
	})
}

const testAccFederationIssuersDataSourceConfig = `
data "anthropic_federation_issuers" "test" {}
`
