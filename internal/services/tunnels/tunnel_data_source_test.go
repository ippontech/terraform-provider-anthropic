// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package tunnels_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

const testAccTunnelDataSourceConfig = `
resource "anthropic_tunnel" "test" {
  display_name = "tf-acc-test-tunnel-ds"
}

data "anthropic_tunnel" "test" {
  id = anthropic_tunnel.test.id
}
`

// TestAccTunnelDataSource_basic reads back a tunnel created in the same
// configuration. See preCheckTunnelsAcc for why it only runs on explicit
// opt-in.
func TestAccTunnelDataSource_basic(t *testing.T) {
	preCheckTunnelsAcc(t)

	const ds = "data.anthropic_tunnel.test"
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheckTunnelsAcc(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckTunnelsArchived,
		Steps: []resource.TestStep{
			{
				Config: testAccTunnelDataSourceConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrPair(ds, "id", "anthropic_tunnel.test", "id"),
					resource.TestCheckResourceAttrPair(ds, "domain", "anthropic_tunnel.test", "domain"),
					resource.TestCheckResourceAttr(ds, "display_name", "tf-acc-test-tunnel-ds"),
					resource.TestCheckResourceAttrSet(ds, "created_at"),
					resource.TestCheckNoResourceAttr(ds, "archived_at"),
					resource.TestCheckNoResourceAttr(ds, "token"),
				),
			},
		},
	})
}
