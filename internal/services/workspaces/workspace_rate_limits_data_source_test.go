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

// With include_inherited the workspace sees every group that has
// organization-level limits, so the list is expected to be non-empty even
// when the terraform-tests workspace has no override at all.
func TestAccWorkspaceRateLimitsDataSource_includeInherited(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckAdmin(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccWorkspaceRateLimitsDataSourceConfigInherited,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.anthropic_workspace_rate_limits.test", "rate_limits.0.rate_limit_id"),
					resource.TestCheckResourceAttrSet("data.anthropic_workspace_rate_limits.test", "rate_limits.0.group.id"),
					resource.TestCheckResourceAttrSet("data.anthropic_workspace_rate_limits.test", "rate_limits.0.limits.0.source.type"),
				),
			},
		},
	})
}

const testAccWorkspaceRateLimitsDataSourceConfigInherited = `
data "anthropic_workspace_rate_limits" "test" {
  workspace_id      = "` + acctest.TerraformTestsWorkspaceID + `"
  include_inherited = true
}
`

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
