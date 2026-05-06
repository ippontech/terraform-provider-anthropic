// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces_test

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

func TestAccWorkspaceMemberResource_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccWorkspaceMemberConfig("workspace_user"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("anthropic_workspace_member.test", "workspace_role", "workspace_user"),
					resource.TestCheckResourceAttr("anthropic_workspace_member.test", "type", "workspace_member"),
					resource.TestCheckResourceAttrSet("anthropic_workspace_member.test", "id"),
				),
			},
			{
				ResourceName:      "anthropic_workspace_member.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func TestAccWorkspaceMemberResource_updateRole(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccWorkspaceMemberConfig("workspace_user"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("anthropic_workspace_member.test", "workspace_role", "workspace_user"),
				),
			},
			{
				Config: testAccWorkspaceMemberConfig("workspace_admin"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttr("anthropic_workspace_member.test", "workspace_role", "workspace_admin"),
				),
			},
		},
	})
}

func TestAccWorkspaceMemberResource_rejectsBillingOnCreate(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      testAccWorkspaceMemberConfig("workspace_billing"),
				ExpectError: regexp.MustCompile(`workspace_billing`),
			},
		},
	})
}

func TestAccWorkspaceMemberResource_rejectsBillingOnUpdate(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccWorkspaceMemberConfig("workspace_user"),
			},
			{
				Config:      testAccWorkspaceMemberConfig("workspace_billing"),
				ExpectError: regexp.MustCompile(`workspace_billing`),
			},
		},
	})
}

func testAccWorkspaceMemberConfig(role string) string {
	return `
resource "anthropic_workspace" "test" {
  name = "tf-acc-workspace-member-test"
}

resource "anthropic_workspace_member" "test" {
  workspace_id   = anthropic_workspace.test.id
  user_id        = "user_01test"
  workspace_role = "` + role + `"
}
`
}
