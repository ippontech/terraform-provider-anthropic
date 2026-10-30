// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package tunnels_test

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

const certAddr = "anthropic_tunnel_certificate.test"

// certConfig is a tunnel and a certificate on it.
func certConfig(pemText string) string {
	return tunnelConfig("") + fmt.Sprintf(`
resource "anthropic_tunnel_certificate" "test" {
  tunnel_id          = anthropic_tunnel.test.id
  ca_certificate_pem = %q
}
`, pemText)
}

// notAfter is the expiry the API reports for the certificate in pemText.
func notAfter(t *testing.T, pemText string) string {
	t.Helper()
	block, _ := pem.Decode([]byte(pemText))
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return cert.NotAfter.UTC().Format(time.RFC3339)
}

// rewrap re-encodes the certificate in pemText with 40-column lines and CRLF
// endings: other text, same DER, same fingerprint.
func rewrap(pemText string) string {
	block, _ := pem.Decode([]byte(pemText))
	b64 := base64.StdEncoding.EncodeToString(block.Bytes)
	var lines []string
	for len(b64) > 40 {
		lines, b64 = append(lines, b64[:40]), b64[40:]
	}
	lines = append(lines, b64)
	return "-----BEGIN CERTIFICATE-----\r\n" + strings.Join(lines, "\r\n") + "\r\n-----END CERTIFICATE-----\r\n"
}

func expectCert(t *testing.T, n int, pemText, fingerprint, createdAt string) []statecheck.StateCheck {
	t.Helper()
	check := func(attr string, c knownvalue.Check) statecheck.StateCheck {
		return statecheck.ExpectKnownValue(certAddr, tfjsonpath.New(attr), c)
	}
	return []statecheck.StateCheck{
		check("id", knownvalue.StringExact(certID(n))),
		check("tunnel_id", knownvalue.StringExact(tunnelID(1))),
		check("ca_certificate_pem", knownvalue.StringExact(pemText)),
		check("fingerprint", knownvalue.StringExact(fingerprint)),
		check("created_at", knownvalue.StringExact(createdAt)),
		check("expires_at", knownvalue.StringExact(notAfter(t, pemText))),
	}
}

// TestTunnelCertificateResource_lifecycle registers a CA, imports it,
// applies a re-wrapped PEM of the same certificate in place without a
// request, replaces it with another CA, and archives everything.
func TestTunnelCertificateResource_lifecycle(t *testing.T) {
	t.Parallel()
	srv := newTunnelsFake(t, 0)
	ca1, fp1 := testAccCA(t, "first")
	ca2, fp2 := testAccCA(t, "second")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkAllArchived,
		Steps: []resource.TestStep{
			{
				Config:            certConfig(ca1),
				ConfigPlanChecks:  expectAction(certAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectCert(t, 1, ca1, fp1, at(1)),
			},
			{
				Config:                  certConfig(ca1),
				ResourceName:            certAddr,
				ImportState:             true,
				ImportStateId:           tunnelID(1) + ":" + certID(1),
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"ca_certificate_pem"},
			},
			{
				Config:            certConfig(rewrap(ca1)),
				ConfigPlanChecks:  expectAction(certAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectCert(t, 1, rewrap(ca1), fp1, at(1)),
			},
			{
				Config:            certConfig(ca2),
				ConfigPlanChecks:  expectAction(certAddr, plancheck.ResourceActionReplace),
				ConfigStateChecks: expectCert(t, 2, ca2, fp2, at(3)),
			},
		},
	})

	srv.AssertJSONBodies(routeCreateCert,
		fmt.Sprintf(`{"ca_certificate_pem": %q}`, ca1),
		fmt.Sprintf(`{"ca_certificate_pem": %q}`, ca2))
	want := []string{
		"/v1/tunnels/" + tunnelID(1) + "/certificates/" + certID(1) + "/archive ",
		"/v1/tunnels/" + tunnelID(1) + "/certificates/" + certID(2) + "/archive ",
	}
	if got := srv.requestPaths(routeArchiveCert); !slices.Equal(got, want) {
		t.Errorf("certificate archives %q, want %q", got, want)
	}
	srv.assertRequestShape(t)
}

// TestTunnelCertificateResource_goneOutsideTerraform: a certificate
// archived behind Terraform's back, then one deleted (404), leave the state
// and are registered again; a 404 on the archive of the destroy is success.
func TestTunnelCertificateResource_goneOutsideTerraform(t *testing.T) {
	t.Parallel()
	srv := newTunnelsFake(t, 0)
	ca, _ := testAccCA(t, "gone")
	config := certConfig(ca)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{Config: config, ConfigPlanChecks: expectAction(certAddr, plancheck.ResourceActionCreate)},
			{
				PreConfig:        func() { srv.certs.Archive(certID(1)) },
				Config:           config,
				ConfigPlanChecks: expectAction(certAddr, plancheck.ResourceActionCreate),
			},
			{
				PreConfig:        func() { srv.certs.Delete(certID(2)) },
				Config:           config,
				ConfigPlanChecks: expectAction(certAddr, plancheck.ResourceActionCreate),
			},
			{
				PreConfig:        func() { srv.FailNext(routeArchiveCert, http.StatusNotFound, 1) },
				Config:           tunnelConfig(""),
				ConfigPlanChecks: expectAction(certAddr, plancheck.ResourceActionDestroy),
			},
		},
	})

	srv.AssertCalled(routeCreateCert, 3)
	if got, want := srv.requestPaths(routeArchiveCert), []string{
		"/v1/tunnels/" + tunnelID(1) + "/certificates/" + certID(3) + "/archive status 404",
	}; !slices.Equal(got, want) {
		t.Errorf("certificate archives %q, want %q", got, want)
	}
	srv.assertRequestShape(t)
}

// TestTunnelCertificateResource_errors: a third live certificate is
// refused by the API, an invalid PEM by the validator before any request,
// and an API error on refresh or archive reaches the operator.
func TestTunnelCertificateResource_errors(t *testing.T) {
	t.Parallel()
	srv := newTunnelsFake(t, 0)
	ca, _ := testAccCA(t, "third")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkAllArchived,
		Steps: []resource.TestStep{
			{
				Config:      certConfig("not a certificate"),
				ExpectError: regexp.MustCompile(`(?s)Invalid\s+CA\s+certificate.*no\s+PEM\s+block`),
			},
			{
				Config: tunnelConfig(""),
				Check: func(*terraform.State) error {
					// Two certificates registered outside this configuration.
					for range 2 {
						srv.certs.Create(map[string]any{"type": "tunnel_certificate", "tunnel_id": tunnelID(1), "fingerprint": "00", "archived_at": nil})
					}
					return nil
				},
			},
			{
				Config:      certConfig(ca),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+register\s+tunnel\s+certificate:.*409.*already\s+has\s+two`),
			},
			{
				PreConfig:        func() { srv.certs.Archive(certID(1)) },
				Config:           certConfig(ca),
				ConfigPlanChecks: expectAction(certAddr, plancheck.ResourceActionCreate),
			},
			{
				PreConfig:   func() { srv.FailNext(routeGetCert, http.StatusForbidden, 1) },
				Config:      certConfig(ca),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+read\s+tunnel\s+certificate:.*403`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeArchiveCert, http.StatusBadRequest, 1) },
				Config:      tunnelConfig(""),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+archive\s+tunnel\s+certificate:.*400`),
			},
		},
	})

	srv.AssertCalled(routeCreateCert, 2)
	srv.assertRequestShape(t)
}

// TestTunnelCertificateResource_importInvalidID: an import ID that is not
// <tunnel_id>:<certificate_id> is refused before any request.
func TestTunnelCertificateResource_importInvalidID(t *testing.T) {
	t.Parallel()
	ca, _ := testAccCA(t, "import")
	for _, id := range []string{"tcrt_only", ":tcrt_1", "tnl_1:"} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			srv := newTunnelsFake(t, 0)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
				Steps: []resource.TestStep{{
					Config: providertest.ProviderConfig(sdktest.AuthToken) + fmt.Sprintf(`
resource "anthropic_tunnel_certificate" "test" {
  tunnel_id          = "tnl_1"
  ca_certificate_pem = %q
}
`, ca),
					ResourceName:  certAddr,
					ImportState:   true,
					ImportStateId: id,
					ExpectError:   regexp.MustCompile(`Invalid\s+import\s+ID`),
				}},
			})
			srv.AssertCalled("", 0)
		})
	}
}
