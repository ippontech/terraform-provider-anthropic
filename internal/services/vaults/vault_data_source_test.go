// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package vaults_test

import (
	"testing"

	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// The vault data source tests create their fixture in a first step and look it
// up in a second one rather than in the same configuration, so the lookup
// never races the read-after-write window measured on the vaults API (see
// CLAUDE.md). Vaults are free to create and destroy.

const testAccVaultDataSourceFixture = `
resource "anthropic_vault" "test" {
  display_name = "tf-acc-test-vault-ds"

  metadata = {
    purpose = "acctest"
  }
}
`

const testAccVaultDataSourceConfig = testAccVaultDataSourceFixture + `
data "anthropic_vault" "test" {
  id = anthropic_vault.test.id
}
`

func TestAccVaultDataSource_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckVaultDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccVaultDataSourceFixture,
			},
			{
				Config: testAccVaultDataSourceConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair("data.anthropic_vault.test", "id", "anthropic_vault.test", "id"),
					resource.TestCheckResourceAttr("data.anthropic_vault.test", "display_name", "tf-acc-test-vault-ds"),
					resource.TestCheckResourceAttr("data.anthropic_vault.test", "metadata.purpose", "acctest"),
					resource.TestCheckResourceAttr("data.anthropic_vault.test", "type", "vault"),
					resource.TestCheckResourceAttrPair("data.anthropic_vault.test", "created_at", "anthropic_vault.test", "created_at"),
					resource.TestCheckNoResourceAttr("data.anthropic_vault.test", "archived_at"),
				),
			},
		},
	})
}
