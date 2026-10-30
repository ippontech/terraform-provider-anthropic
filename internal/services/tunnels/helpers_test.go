// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package tunnels_test

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

// The tunnels API takes an OAuth bearer token with the
// workspace:manage_tunnels scope, never an API key, so these tests are gated
// on acctest.PreCheckOAuth (ANTHROPIC_AUTH_TOKEN). That alone is not enough:
// CI has no such token, the only bearer available locally is an org:admin
// one (a different scope), MCP tunnels are a research preview the test
// organization must be granted, and nothing they create can be deleted —
// every run leaves archived tunnels and certificates behind, and holds some
// of the organization's 10 active-tunnel slots while it runs. So they also
// require an explicit ANTHROPIC_TUNNELS_ACC=1 opt-in, with
// ANTHROPIC_AUTH_TOKEN set to a workspace:manage_tunnels token.
func preCheckTunnelsAcc(t *testing.T) {
	t.Helper()
	acctest.PreCheckOAuth(t)
	if os.Getenv("ANTHROPIC_TUNNELS_ACC") == "" {
		t.Skip("skipping: ANTHROPIC_TUNNELS_ACC is not set. Tunnel tests need ANTHROPIC_AUTH_TOKEN to carry the " +
			"workspace:manage_tunnels scope, and every run leaves archived (undeletable) tunnels behind.")
	}
}

// The tunnels endpoints have not been probed for the read-after-write
// staleness measured on the vaults API, so CheckDestroy polls (same bounds as
// the vaults and service account helpers) rather than trusting one Get.
const (
	tunnelDestroyCheckTimeout  = 5 * time.Second
	tunnelDestroyCheckInterval = 200 * time.Millisecond
)

// awaitArchived polls get until it reports a non-zero archived_at. A read
// error fails immediately: the object existed before the archive, and the API
// keeps archived records, so a stale read shows the live object, never an
// error.
func awaitArchived(what string, get func() (time.Time, error)) error {
	deadline := time.Now().Add(tunnelDestroyCheckTimeout)
	for {
		archivedAt, err := get()
		if err != nil {
			return fmt.Errorf("%s not readable after destroy: %w", what, err)
		}
		if !archivedAt.IsZero() {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s was not archived on destroy", what)
		}
		time.Sleep(tunnelDestroyCheckInterval)
	}
}

// testAccCheckTunnelsArchived is the CheckDestroy of every tunnels test:
// destroy archives, so each tunnel and certificate must still be readable,
// with archived_at set.
func testAccCheckTunnelsArchived(s *terraform.State) error {
	client := acctest.NewOAuthClient()
	ctx := context.Background()
	for _, rs := range s.RootModule().Resources {
		switch rs.Type {
		case "anthropic_tunnel":
			id := rs.Primary.ID
			if err := awaitArchived("tunnel "+id, func() (time.Time, error) {
				tunnel, err := client.Beta.Tunnels.Get(ctx, id, anthropic.BetaTunnelGetParams{})
				if err != nil {
					return time.Time{}, err
				}
				return tunnel.ArchivedAt, nil
			}); err != nil {
				return err
			}
		case "anthropic_tunnel_certificate":
			id, tunnelID := rs.Primary.ID, rs.Primary.Attributes["tunnel_id"]
			if err := awaitArchived("tunnel certificate "+id, func() (time.Time, error) {
				cert, err := client.Beta.Tunnels.Certificates.Get(ctx, id, anthropic.BetaTunnelCertificateGetParams{TunnelID: tunnelID})
				if err != nil {
					return time.Time{}, err
				}
				return cert.ArchivedAt, nil
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

// testAccCA returns a freshly generated self-signed CA certificate (PEM) that
// meets the API's requirements (ECDSA P-256, critical BasicConstraints
// CA:TRUE, SubjectKeyIdentifier, keyCertSign, currently valid), and the hex
// SHA-256 of its DER encoding, which the API reports as fingerprint. The
// private key is discarded: the tunnel only ever sees the public CA.
func testAccCA(t *testing.T, cn string) (pemText, fingerprint string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(30 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	sum := sha256.Sum256(der)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), hex.EncodeToString(sum[:])
}
