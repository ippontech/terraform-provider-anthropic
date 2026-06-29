// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package organizations_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

// Read-only smoke test for the single organization member data source. The org
// always has at least one member, so the test chains off the members list to
// resolve a real user ID and reads it back by ID. The deterministic mapping and
// 404 handling stay covered by the httptest-based unit tests in package
// organizations.
func TestAccOrganizationMemberDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckAdmin(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
data "anthropic_organization_members" "all" {}

data "anthropic_organization_member" "first" {
  id = data.anthropic_organization_members.all.members[0].id
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.anthropic_organization_member.first", "email"),
					resource.TestCheckResourceAttr("data.anthropic_organization_member.first", "type", "user"),
				),
			},
		},
	})
}
