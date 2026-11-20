// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package tunnels_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

const testAccTunnelsDataSourceConfig = `
resource "anthropic_tunnel" "test" {
  display_name = "tf-acc-test-tunnels-ds"
}

data "anthropic_tunnels" "test" {
  depends_on = [anthropic_tunnel.test]
}
`

// TestAccTunnelsDataSource_basic is a smoke test: the workspace may hold other
// tunnels, so it only asserts that the one it created is listed. See
// preCheckTunnelsAcc for why it only runs on explicit opt-in.
func TestAccTunnelsDataSource_basic(t *testing.T) {
	preCheckTunnelsAcc(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheckTunnelsAcc(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckTunnelsArchived,
		Steps: []resource.TestStep{
			{
				Config: testAccTunnelsDataSourceConfig,
				Check: resource.TestCheckTypeSetElemNestedAttrs("data.anthropic_tunnels.test", "tunnels.*", map[string]string{
					"display_name": "tf-acc-test-tunnels-ds",
				}),
			},
		},
	})
}
