// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package memorystores_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

const testAccMemoryStoreDataSourceConfig = `
resource "anthropic_memory_store" "test" {
  name        = "tf-acc-test-memstore-ds"
  description = "tf acceptance test store for the singular data source"
}

data "anthropic_memory_store" "test" {
  id = anthropic_memory_store.test.id
}
`

func TestAccMemoryStoreDataSource_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckMemoryStoreDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccMemoryStoreDataSourceConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair("data.anthropic_memory_store.test", "id", "anthropic_memory_store.test", "id"),
					resource.TestCheckResourceAttr("data.anthropic_memory_store.test", "name", "tf-acc-test-memstore-ds"),
					resource.TestCheckResourceAttr("data.anthropic_memory_store.test", "description", "tf acceptance test store for the singular data source"),
					resource.TestCheckNoResourceAttr("data.anthropic_memory_store.test", "archived_at"),
				),
			},
		},
	})
}
