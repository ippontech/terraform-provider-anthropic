// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package memorystores_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

const testAccMemoryStoresDataSourceConfig = `
resource "anthropic_memory_store" "test" {
  name        = "tf-acc-test-memstore-list"
  description = "tf acceptance test store for the plural data source"
}

data "anthropic_memory_stores" "test" {
  depends_on = [anthropic_memory_store.test]
}
`

func TestAccMemoryStoresDataSource_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckMemoryStoreDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccMemoryStoresDataSourceConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.anthropic_memory_stores.test", "memory_stores.#"),
				),
			},
		},
	})
}
