// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces_test

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

// Read-only smoke test for the members list of the terraform-tests workspace.
// Transparent pagination and the empty-workspace path stay covered by the
// httptest-based unit tests; this exercises the live Admin API round-trip.
func TestAccWorkspaceMembersDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckAdmin(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`data "anthropic_workspace_members" "test" { workspace_id = %q }`, acctest.TerraformTestsWorkspaceID),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.anthropic_workspace_members.test", "workspace_id", acctest.TerraformTestsWorkspaceID),
					resource.TestCheckResourceAttrSet("data.anthropic_workspace_members.test", "members.#"),
				),
			},
		},
	})
}
