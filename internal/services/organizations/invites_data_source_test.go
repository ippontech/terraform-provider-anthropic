// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package organizations_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

// Read-only smoke test for the invites list. Invites are org-wide (not
// workspace-scoped), and the test org may have none pending, so this only
// asserts invites.# is present, not a specific count. Pagination and the
// email/statuses filters stay covered deterministically by the httptest-based
// unit tests in package organizations.
func TestAccInvitesDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckAdmin(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `data "anthropic_invites" "all" {}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.anthropic_invites.all", "invites.#"),
				),
			},
		},
	})
}

// No smoke test for the single anthropic_invite data source: unlike
// organization_member (which can chain off organization_members to resolve a
// real user ID), there is no deterministically valid invite ID to look up
// without inviting a real person's email during a test run. Its mapping and
// 404 handling stay covered by the httptest-based unit tests in package
// organizations.
