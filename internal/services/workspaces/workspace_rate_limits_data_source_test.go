// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

func TestAccWorkspaceRateLimitsDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckAdmin(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccWorkspaceRateLimitsDataSourceConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.anthropic_workspace_rate_limits.test", "rate_limits.#"),
					resource.TestCheckResourceAttr("data.anthropic_workspace_rate_limits.test", "workspace_id", acctest.TerraformTestsWorkspaceID),
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
  workspace_id = "` + acctest.TerraformTestsWorkspaceID + `"
}
`

const testAccWorkspaceRateLimitsDataSourceConfigGroupType = `
data "anthropic_workspace_rate_limits" "test" {
  workspace_id = "` + acctest.TerraformTestsWorkspaceID + `"
  group_type   = "model_group"
}
`
