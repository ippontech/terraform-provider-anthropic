// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package tunnels_test

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

func testAccTunnelCertificatesDataSourceConfig(caPEM string) string {
	return fmt.Sprintf(`
resource "anthropic_tunnel" "test" {
  display_name = "tf-acc-test-tunnel-certificates-ds"
}

resource "anthropic_tunnel_certificate" "test" {
  tunnel_id          = anthropic_tunnel.test.id
  ca_certificate_pem = <<-EOT
%sEOT
}

data "anthropic_tunnel_certificates" "test" {
  tunnel_id  = anthropic_tunnel.test.id
  depends_on = [anthropic_tunnel_certificate.test]
}
`, caPEM)
}

// TestAccTunnelCertificatesDataSource_basic lists the certificates of a
// fresh tunnel, which holds only the one it registered. See
// preCheckTunnelsAcc for why it only runs on explicit opt-in.
func TestAccTunnelCertificatesDataSource_basic(t *testing.T) {
	preCheckTunnelsAcc(t)

	caPEM, fingerprint := testAccCA(t, "tf-acc-test-ca-list")
	const name = "data.anthropic_tunnel_certificates.test"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheckTunnelsAcc(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckTunnelsArchived,
		Steps: []resource.TestStep{
			{
				Config: testAccTunnelCertificatesDataSourceConfig(caPEM),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(name, "certificates.#", "1"),
					resource.TestCheckResourceAttrPair(name, "certificates.0.id", "anthropic_tunnel_certificate.test", "id"),
					resource.TestCheckResourceAttrPair(name, "certificates.0.tunnel_id", "anthropic_tunnel.test", "id"),
					resource.TestCheckResourceAttr(name, "certificates.0.fingerprint", fingerprint),
					resource.TestCheckNoResourceAttr(name, "certificates.0.archived_at"),
				),
			},
		},
	})
}
