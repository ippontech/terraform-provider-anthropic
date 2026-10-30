// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package tunnels_test

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

func testAccTunnelResourceConfig(rotation string) string {
	return fmt.Sprintf(`
resource "anthropic_tunnel" "test" {
  display_name   = "tf-acc-test-tunnel"
  token_rotation = %q
}
`, rotation)
}

// TestAccTunnelResource_basic creates a tunnel, rotates its token through
// token_rotation, and imports it. See preCheckTunnelsAcc for why it only runs
// on explicit opt-in.
func TestAccTunnelResource_basic(t *testing.T) {
	preCheckTunnelsAcc(t)

	const name = "anthropic_tunnel.test"
	var firstTokenID string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheckTunnelsAcc(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckTunnelsArchived,
		Steps: []resource.TestStep{
			{
				Config: testAccTunnelResourceConfig("1"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestMatchResourceAttr(name, "id", regexp.MustCompile(`^tnl_`)),
					resource.TestCheckResourceAttr(name, "display_name", "tf-acc-test-tunnel"),
					resource.TestCheckResourceAttrSet(name, "domain"),
					resource.TestCheckResourceAttrSet(name, "created_at"),
					resource.TestCheckResourceAttrSet(name, "token"),
					resource.TestCheckResourceAttrSet(name, "token_id"),
					captureAttr(name, "token_id", &firstTokenID),
				),
			},
			{
				// A new token_rotation value rotates the token in place.
				Config: testAccTunnelResourceConfig("2"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet(name, "token"),
					checkAttrChanged(name, "token_id", &firstTokenID),
				),
			},
			{
				// Import reveals the current token once; token_rotation is
				// configuration only.
				ResourceName:            name,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"token_rotation"},
			},
		},
	})
}

// captureAttr stores the current value of an attribute for a later step.
func captureAttr(name, key string, into *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[name]
		if !ok {
			return fmt.Errorf("resource %s not found in state", name)
		}
		*into = rs.Primary.Attributes[key]
		return nil
	}
}

// checkAttrChanged fails unless the attribute differs from the captured
// value.
func checkAttrChanged(name, key string, previous *string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[name]
		if !ok {
			return fmt.Errorf("resource %s not found in state", name)
		}
		if got := rs.Primary.Attributes[key]; got == *previous {
			return fmt.Errorf("%s.%s = %q, expected it to change", name, key, got)
		}
		return nil
	}
}
