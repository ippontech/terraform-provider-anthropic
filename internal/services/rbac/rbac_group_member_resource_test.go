// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package rbac_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

// The member is the first user of the organization, added to a group the test
// creates (with no roles, so the membership grants nothing) and removed with it.
const testAccRBACGroupMemberConfig = `
data "anthropic_organization_members" "all" {}

resource "anthropic_rbac_group" "test" {
  name = "tf-acc-rbac-group-member"
}

resource "anthropic_rbac_group_member" "test" {
  rbac_group_id = anthropic_rbac_group.test.id
  user_id       = data.anthropic_organization_members.all.members[0].id
}
`

func TestAccRBACGroupMemberResource_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheckEnterpriseAcc(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckRBACGroupDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccRBACGroupMemberConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair("anthropic_rbac_group_member.test", "rbac_group_id", "anthropic_rbac_group.test", "id"),
					resource.TestCheckResourceAttrSet("anthropic_rbac_group_member.test", "email"),
					resource.TestCheckResourceAttrSet("anthropic_rbac_group_member.test", "created_at"),
				),
			},
			{
				ResourceName:      "anthropic_rbac_group_member.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}
