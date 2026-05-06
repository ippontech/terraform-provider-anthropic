// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

// terraformTestsWorkspaceID is the ID of the "terraform-tests" workspace.
// Read-only Admin API data source acceptance tests target it because we do
// not yet have a dedicated test organisation for create/update/delete flows.
const terraformTestsWorkspaceID = "wrkspc_01HMrPGQfWoZ5LnhFhxuvNsm"

func TestAccWorkspaceRateLimitsDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckAdmin(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccWorkspaceRateLimitsDataSourceConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.anthropic_workspace_rate_limits.test", "rate_limits.#"),
					resource.TestCheckResourceAttr("data.anthropic_workspace_rate_limits.test", "workspace_id", terraformTestsWorkspaceID),
				),
			},
		},
	})
}

func TestAccWorkspaceRateLimitsDataSource_groupTypeFilter(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckAdmin(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccWorkspaceRateLimitsDataSourceConfigGroupType,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.anthropic_workspace_rate_limits.test", "rate_limits.#"),
					resource.TestCheckResourceAttr("data.anthropic_workspace_rate_limits.test", "group_type", "model_group"),
				),
			},
		},
	})
}

const testAccWorkspaceRateLimitsDataSourceConfig = `
data "anthropic_workspace_rate_limits" "test" {
  workspace_id = "` + terraformTestsWorkspaceID + `"
}
`

const testAccWorkspaceRateLimitsDataSourceConfigGroupType = `
data "anthropic_workspace_rate_limits" "test" {
  workspace_id = "` + terraformTestsWorkspaceID + `"
  group_type   = "model_group"
}
`
