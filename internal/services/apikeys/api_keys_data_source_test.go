// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package apikeys_test

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

// Read-only smoke test for the API keys list scoped to the terraform-tests
// workspace. Pagination and query-param/filter construction stay covered by the
// httptest-based unit tests in package apikeys; this exercises the live Admin
// API round-trip with the workspace_id filter.
func TestAccAPIKeysDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckAdmin(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`data "anthropic_api_keys" "test" { workspace_id = %q }`, acctest.TerraformTestsWorkspaceID),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.anthropic_api_keys.test", "api_keys.#"),
				),
			},
		},
	})
}
