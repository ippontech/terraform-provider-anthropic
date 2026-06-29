// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

// Read-only smoke test for the list data source. Transparent pagination is
// covered deterministically by the httptest-based unit tests; this asserts the
// live Admin API list round-trip populates the collection (the terraform-tests
// workspace itself guarantees at least one entry).
func TestAccWorkspacesDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckAdmin(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `data "anthropic_workspaces" "all" {}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.anthropic_workspaces.all", "workspaces.#"),
				),
			},
		},
	})
}
