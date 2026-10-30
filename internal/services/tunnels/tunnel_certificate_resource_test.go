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

func testAccTunnelCertificateConfig(caPEM string) string {
	return fmt.Sprintf(`
resource "anthropic_tunnel" "test" {
  display_name = "tf-acc-test-tunnel-certificate"
}

resource "anthropic_tunnel_certificate" "test" {
  tunnel_id          = anthropic_tunnel.test.id
  ca_certificate_pem = <<-EOT
%sEOT
}
`, caPEM)
}

// TestAccTunnelCertificateResource_basic registers a CA, replaces it with
// another one, and imports the result. See preCheckTunnelsAcc for why it only
// runs on explicit opt-in.
func TestAccTunnelCertificateResource_basic(t *testing.T) {
	preCheckTunnelsAcc(t)

	firstPEM, firstFingerprint := testAccCA(t, "tf-acc-test-ca-1")
	secondPEM, secondFingerprint := testAccCA(t, "tf-acc-test-ca-2")
	const name = "anthropic_tunnel_certificate.test"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheckTunnelsAcc(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckTunnelsArchived,
		Steps: []resource.TestStep{
			{
				Config: testAccTunnelCertificateConfig(firstPEM),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestMatchResourceAttr(name, "id", regexp.MustCompile(`^tcrt_`)),
					resource.TestCheckResourceAttrPair(name, "tunnel_id", "anthropic_tunnel.test", "id"),
					resource.TestCheckResourceAttr(name, "fingerprint", firstFingerprint),
					resource.TestCheckResourceAttrSet(name, "created_at"),
					resource.TestCheckResourceAttrSet(name, "expires_at"),
				),
			},
			{
				// A different CA replaces the certificate.
				Config: testAccTunnelCertificateConfig(secondPEM),
				Check:  resource.TestCheckResourceAttr(name, "fingerprint", secondFingerprint),
			},
			{
				// The API never returns the PEM; the next plan adopts the
				// configured one in place because its fingerprint matches.
				ResourceName:            name,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateIdFunc:       tunnelCertificateImportID(name),
				ImportStateVerifyIgnore: []string{"ca_certificate_pem"},
			},
		},
	})
}

func tunnelCertificateImportID(name string) resource.ImportStateIdFunc {
	return func(s *terraform.State) (string, error) {
		rs, ok := s.RootModule().Resources[name]
		if !ok {
			return "", fmt.Errorf("resource %s not found in state", name)
		}
		return rs.Primary.Attributes["tunnel_id"] + ":" + rs.Primary.ID, nil
	}
}
