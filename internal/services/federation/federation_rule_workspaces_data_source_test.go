// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

// TestAccFederationRuleWorkspacesDataSource is a read-only smoke test against
// a live org:admin OAuth session. The mapping/pagination/404 logic stays
// covered by the httptest-based unit tests in package federation; this only
// exercises the live round-trip.
//
// No test org exists yet for WIF resource writes (same blocker as #58) and CI
// has no durable org:admin token, so this is gated on acctest.PreCheckOAuth
// and runs locally only. The rule ID is resolved from the anthropic_federation_rules
// list data source rather than a hardcoded constant, since no fixed ID is
// deterministically valid across orgs.
func TestAccFederationRuleWorkspacesDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckOAuth(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccFederationRuleWorkspacesDataSourceConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.anthropic_federation_rule_workspaces.test", "workspaces.#"),
				),
			},
		},
	})
}

const testAccFederationRuleWorkspacesDataSourceConfig = `
data "anthropic_federation_rules" "all" {}

data "anthropic_federation_rule_workspaces" "test" {
  federation_rule_id = data.anthropic_federation_rules.all.rules[0].id
}
`
