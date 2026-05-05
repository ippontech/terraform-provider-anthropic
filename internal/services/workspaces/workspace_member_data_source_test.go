// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

func TestAccWorkspaceMemberDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccWorkspaceMemberDataSourceConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.anthropic_workspace_member.test", "workspace_role"),
					resource.TestCheckResourceAttrSet("data.anthropic_workspace_member.test", "type"),
				),
			},
		},
	})
}

const testAccWorkspaceMemberDataSourceConfig = `
variable "workspace_id" {}
variable "user_id" {}

data "anthropic_workspace_member" "test" {
  workspace_id = var.workspace_id
  user_id      = var.user_id
}
`
