// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces_test

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

// Read-only smoke test against the dedicated terraform-tests workspace. The
// pagination/mapping/edge-case logic stays covered by the httptest-based unit
// tests in package workspaces; this only exercises the live Admin API round-trip.
func TestAccWorkspaceDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckAdmin(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`data "anthropic_workspace" "test" { id = %q }`, acctest.TerraformTestsWorkspaceID),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.anthropic_workspace.test", "id", acctest.TerraformTestsWorkspaceID),
					resource.TestCheckResourceAttrSet("data.anthropic_workspace.test", "name"),
					resource.TestCheckResourceAttrSet("data.anthropic_workspace.test", "created_at"),
				),
			},
		},
	})
}
