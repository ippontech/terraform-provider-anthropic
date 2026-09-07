// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package serviceaccounts_test

import (
	"testing"

	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// TestAccServiceAccountsDataSource is a smoke test only: the live organization's
// set of service accounts is not deterministic (no dedicated test org exists
// yet, see #58), so it asserts attribute presence rather than specific values.
func TestAccServiceAccountsDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckOAuth(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccServiceAccountsDataSourceConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.anthropic_service_accounts.test", "service_accounts.#"),
				),
			},
		},
	})
}

const testAccServiceAccountsDataSourceConfig = `
data "anthropic_service_accounts" "test" {}
`
