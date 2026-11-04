// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package vaults_test

import (
	"testing"

	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

const testAccVaultsDataSourceFixture = `
resource "anthropic_vault" "test" {
  display_name = "tf-acc-test-vaults-ds"
}
`

const testAccVaultsDataSourceConfig = testAccVaultsDataSourceFixture + `
data "anthropic_vaults" "test" {
  depends_on = [anthropic_vault.test]
}
`

// The live workspace may hold other vaults, so the list is only checked for
// the fixture created in the first step (see vault_data_source_test.go for
// why the lookup gets its own step).
func TestAccVaultsDataSource_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckVaultDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccVaultsDataSourceFixture,
			},
			{
				Config: testAccVaultsDataSourceConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.anthropic_vaults.test", "vaults.#"),
					resource.TestCheckTypeSetElemAttrPair("data.anthropic_vaults.test", "vaults.*.id", "anthropic_vault.test", "id"),
					resource.TestCheckTypeSetElemNestedAttrs("data.anthropic_vaults.test", "vaults.*", map[string]string{
						"display_name": "tf-acc-test-vaults-ds",
						"type":         "vault",
					}),
				),
			},
		},
	})
}
