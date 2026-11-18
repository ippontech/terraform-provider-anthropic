// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package rbac_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

// Read-only smoke test: the group list is not deterministic, so it asserts
// presence, not values. Pagination and mapping are covered by the httptest
// unit tests.
func TestAccRBACGroupsDataSource_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheckEnterpriseAcc(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `data "anthropic_rbac_groups" "all" {}`,
				Check:  resource.TestCheckResourceAttrSet("data.anthropic_rbac_groups.all", "rbac_groups.#"),
			},
		},
	})
}
