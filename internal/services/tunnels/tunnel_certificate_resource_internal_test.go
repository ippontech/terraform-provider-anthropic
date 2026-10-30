// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package tunnels

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"maps"
	"math/big"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-anthropic/internal/oauthtest"
)

// newTestCA returns a freshly generated self-signed CA certificate (PEM) and
// the hex SHA-256 of its DER encoding, shaped like the CA the API requires:
// ECDSA P-256, critical BasicConstraints CA:TRUE, a SubjectKeyIdentifier
// (derived by crypto/x509) and keyCertSign.
func newTestCA(t *testing.T, cn string) (pemText, fingerprint string) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(time.Now().UnixNano()),
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
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

// rewrapPEM re-encodes the same certificate with 32-column lines and CRLF
// endings: different text, same DER.
func rewrapPEM(t *testing.T, pemText string) string {
	t.Helper()
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		t.Fatal("rewrapPEM: no PEM block")
	}
	b64 := base64.StdEncoding.EncodeToString(block.Bytes)
	var lines []string
	for len(b64) > 32 {
		lines = append(lines, b64[:32])
		b64 = b64[32:]
	}
	lines = append(lines, b64)
	return "-----BEGIN CERTIFICATE-----\r\n" + strings.Join(lines, "\r\n") + "\r\n-----END CERTIFICATE-----\r\n"
}

func TestPemFingerprint(t *testing.T) {
	pemText, want := newTestCA(t, "fingerprint")
	got, err := pemFingerprint(pemText)
	if err != nil {
		t.Fatalf("pemFingerprint: %v", err)
	}
	if got != want {
		t.Errorf("fingerprint = %s, want %s", got, want)
	}

	rewrapped := rewrapPEM(t, pemText)
	if rewrapped == pemText {
		t.Fatal("rewrapPEM did not change the text")
	}
	if got, err := pemFingerprint(rewrapped); err != nil || got != want {
		t.Errorf("fingerprint of re-wrapped PEM = %s (%v), want %s", got, err, want)
	}
}

func TestCACertificatePEMValidator(t *testing.T) {
	ca, _ := newTestCA(t, "valid")
	other, _ := newTestCA(t, "other")
	// A deliberately truncated key, never a usable one: the validator must
	// refuse private-key material whatever its content.
	keyPEM := "-----BEGIN PRIVATE KEY-----\nMIGHAgEAMBMGByqGSM49AgEGCCqGSM49AwEHBG0wawIBAQQg\n-----END PRIVATE KEY-----\n" // gitleaks:allow

	tests := []struct {
		name    string
		value   types.String
		wantErr string
	}{
		{"valid", types.StringValue(ca), ""},
		{"null", types.StringNull(), ""},
		{"unknown", types.StringUnknown(), ""},
		{"not PEM", types.StringValue("not a certificate"), "no PEM block"},
		{"private key", types.StringValue(keyPEM), "private-key material"},
		{"certificate followed by a key", types.StringValue(ca + keyPEM), "more than one PEM block"},
		{"chain of two certificates", types.StringValue(ca + other), "more than one PEM block"},
		{"too large", types.StringValue(ca + strings.Repeat("#", caCertificatePEMMaxBytes)), "at most 8192"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := validator.StringRequest{Path: path.Root("ca_certificate_pem"), ConfigValue: tc.value}
			resp := &validator.StringResponse{}
			caCertificatePEMValidator{}.ValidateString(context.Background(), req, resp)
			if tc.wantErr == "" {
				if resp.Diagnostics.HasError() {
					t.Errorf("unexpected error: %v", resp.Diagnostics)
				}
				return
			}
			if !resp.Diagnostics.HasError() {
				t.Fatalf("expected an error containing %q", tc.wantErr)
			}
			if detail := resp.Diagnostics.Errors()[0].Detail(); !strings.Contains(detail, tc.wantErr) {
				t.Errorf("error %q does not mention %q", detail, tc.wantErr)
			}
		})
	}
}

// TestCACertificateChanged pins when a PEM change replaces the certificate:
// only when it encodes a different certificate.
func TestCACertificateChanged(t *testing.T) {
	r := &TunnelCertificateResource{}
	ca, fingerprint := newTestCA(t, "current")
	other, _ := newTestCA(t, "next")

	tests := []struct {
		name             string
		stateFingerprint string
		plan             types.String
		wantReplace      bool
	}{
		{"same certificate, re-wrapped text", fingerprint, types.StringValue(rewrapPEM(t, ca)), false},
		{"imported (no PEM in state), same certificate", fingerprint, types.StringValue(ca), false},
		{"fingerprint compared case-insensitively", strings.ToUpper(fingerprint), types.StringValue(ca), false},
		{"different certificate", fingerprint, types.StringValue(other), true},
		{"unknown plan value", fingerprint, types.StringUnknown(), true},
		{"unparsable plan value", fingerprint, types.StringValue("garbage"), true},
		{"no fingerprint in state", "", types.StringValue(ca), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			state := map[string]string{"id": "tcrt_01", "tunnel_id": "tnl_01ABC"}
			if tc.stateFingerprint != "" {
				state["fingerprint"] = tc.stateFingerprint
			}
			req := planmodifier.StringRequest{
				State:     resourceState(t, r, state),
				Plan:      resourcePlan(t, r, map[string]string{"tunnel_id": "tnl_01ABC"}),
				PlanValue: tc.plan,
			}
			resp := &stringplanmodifier.RequiresReplaceIfFuncResponse{}
			caCertificateChanged(context.Background(), req, resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
			}
			if resp.RequiresReplace != tc.wantReplace {
				t.Errorf("RequiresReplace = %v, want %v", resp.RequiresReplace, tc.wantReplace)
			}
		})
	}
}

func certificateJSON(archivedAt, expiresAt string) string {
	quote := func(s string) string {
		if s == "" {
			return "null"
		}
		return fmt.Sprintf("%q", s)
	}
	return fmt.Sprintf(`{
		"id": "tcrt_01",
		"type": "tunnel_certificate",
		"archived_at": %s,
		"created_at": "2026-07-01T10:00:00Z",
		"expires_at": %s,
		"fingerprint": "abcd",
		"tunnel_id": "tnl_01ABC"
	}`, quote(archivedAt), quote(expiresAt))
}

func TestTunnelCertificateResource_Create(t *testing.T) {
	srv, log := fakeTunnelsAPI(t, map[string]func(http.ResponseWriter){
		"POST /v1/tunnels/tnl_01ABC/certificates": respond(http.StatusOK, certificateJSON("", "2031-07-01T10:00:00Z")),
	})
	r := &TunnelCertificateResource{client: oauthtest.NewClient(t, srv)}
	ca, _ := newTestCA(t, "create")

	plan := resourcePlan(t, r, map[string]string{"tunnel_id": "tnl_01ABC", "ca_certificate_pem": ca})
	resp := &resource.CreateResponse{State: resourceState(t, r, nil)}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create returned errors: %v", resp.Diagnostics)
	}

	reqs := log.all()
	if len(reqs) != 1 {
		t.Fatalf("expected one request, got %+v", reqs)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(reqs[0].Body), &body); err != nil {
		t.Fatalf("body: %v", err)
	}
	if body["ca_certificate_pem"] != ca {
		t.Errorf("ca_certificate_pem sent = %v, want the configured PEM", body["ca_certificate_pem"])
	}
	if !strings.Contains(reqs[0].Beta, "mcp-tunnels-2026-06-22") {
		t.Errorf("anthropic-beta = %q, want mcp-tunnels-2026-06-22", reqs[0].Beta)
	}

	var data TunnelCertificateResourceModel
	if diags := resp.State.Get(context.Background(), &data); diags.HasError() {
		t.Fatalf("state: %v", diags)
	}
	if data.ID.ValueString() != "tcrt_01" || data.Fingerprint.ValueString() != "abcd" {
		t.Errorf("id/fingerprint = %v/%v", data.ID, data.Fingerprint)
	}
	if data.ExpiresAt.ValueString() != "2031-07-01T10:00:00Z" {
		t.Errorf("expires_at = %v", data.ExpiresAt)
	}
	if data.CACertificatePEM.ValueString() != ca {
		t.Errorf("ca_certificate_pem = %v, want the configured PEM kept", data.CACertificatePEM)
	}
}

// TestTunnelCertificateResource_Create_isNotRetried: a retried registration
// that had landed would take the tunnel's last free certificate slot.
func TestTunnelCertificateResource_Create_isNotRetried(t *testing.T) {
	srv, log := fakeTunnelsAPI(t, map[string]func(http.ResponseWriter){
		"POST /v1/tunnels/tnl_01ABC/certificates": respond(http.StatusBadGateway, `{"type":"error","error":{"type":"api_error","message":"boom"}}`),
	})
	r := &TunnelCertificateResource{client: oauthtest.NewClient(t, srv)}
	ca, _ := newTestCA(t, "retry")

	resp := &resource.CreateResponse{State: resourceState(t, r, nil)}
	r.Create(context.Background(), resource.CreateRequest{Plan: resourcePlan(t, r, map[string]string{"tunnel_id": "tnl_01ABC", "ca_certificate_pem": ca})}, resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error")
	}
	if n := log.count(http.MethodPost, "/v1/tunnels/tnl_01ABC/certificates"); n != 1 {
		t.Errorf("certificate POST sent %d times, want exactly 1", n)
	}
}

func TestTunnelCertificateResource_Read(t *testing.T) {
	tests := []struct {
		name        string
		routes      map[string]func(http.ResponseWriter)
		wantRemoved bool
	}{
		{"live, never-expiring", map[string]func(http.ResponseWriter){
			"GET /v1/tunnels/tnl_01ABC/certificates/tcrt_01": respond(http.StatusOK, certificateJSON("", "")),
		}, false},
		{"archived outside Terraform", map[string]func(http.ResponseWriter){
			"GET /v1/tunnels/tnl_01ABC/certificates/tcrt_01": respond(http.StatusOK, certificateJSON("2026-07-02T10:00:00Z", "")),
		}, true},
		{"missing", nil, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			srv, log := fakeTunnelsAPI(t, tc.routes)
			r := &TunnelCertificateResource{client: oauthtest.NewClient(t, srv)}

			st := resourceState(t, r, map[string]string{"id": "tcrt_01", "tunnel_id": "tnl_01ABC", "ca_certificate_pem": "kept"})
			resp := &resource.ReadResponse{State: st}
			r.Read(context.Background(), resource.ReadRequest{State: st}, resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("Read returned errors: %v", resp.Diagnostics)
			}
			if n := log.count(http.MethodGet, "/v1/tunnels/tnl_01ABC/certificates/tcrt_01"); n != 1 {
				t.Errorf("GET certificate called %d times, want 1", n)
			}
			if got := resp.State.Raw.IsNull(); got != tc.wantRemoved {
				t.Fatalf("removed from state = %v, want %v", got, tc.wantRemoved)
			}
			if tc.wantRemoved {
				return
			}
			var data TunnelCertificateResourceModel
			resp.State.Get(context.Background(), &data)
			if !data.ExpiresAt.IsNull() {
				t.Errorf("expires_at = %v, want null for a certificate that does not expire", data.ExpiresAt)
			}
			if data.CACertificatePEM.ValueString() != "kept" {
				t.Errorf("ca_certificate_pem = %v, want the state value kept", data.CACertificatePEM)
			}
		})
	}
}

// TestTunnelCertificateResource_Update_noAPICall: Update only stores the
// re-wrapped (same-fingerprint) PEM; nothing changes server-side.
func TestTunnelCertificateResource_Update_noAPICall(t *testing.T) {
	srv, log := fakeTunnelsAPI(t, nil)
	r := &TunnelCertificateResource{client: oauthtest.NewClient(t, srv)}

	computed := map[string]string{"id": "tcrt_01", "tunnel_id": "tnl_01ABC", "fingerprint": "abcd", "created_at": "2026-07-01T10:00:00Z"}
	plan := map[string]string{"ca_certificate_pem": "new text"}
	maps.Copy(plan, computed)
	resp := &resource.UpdateResponse{State: resourceState(t, r, computed)}
	r.Update(context.Background(), resource.UpdateRequest{State: resourceState(t, r, computed), Plan: resourcePlan(t, r, plan)}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Update returned errors: %v", resp.Diagnostics)
	}
	if reqs := log.all(); len(reqs) != 0 {
		t.Errorf("expected no API call, got %+v", reqs)
	}
	var data TunnelCertificateResourceModel
	resp.State.Get(context.Background(), &data)
	if data.CACertificatePEM.ValueString() != "new text" || data.Fingerprint.ValueString() != "abcd" {
		t.Errorf("ca_certificate_pem/fingerprint = %v/%v", data.CACertificatePEM, data.Fingerprint)
	}
}

func TestTunnelCertificateResource_Delete_archives(t *testing.T) {
	for name, tc := range map[string]struct {
		status  int
		body    string
		wantErr bool
	}{
		"archived":     {http.StatusOK, certificateJSON("2026-07-02T10:00:00Z", ""), false},
		"already gone": {http.StatusNotFound, notFoundBody, false},
		"rejected":     {http.StatusBadRequest, `{"type":"error","error":{"type":"invalid_request_error","message":"nope"}}`, true},
	} {
		t.Run(name, func(t *testing.T) {
			srv, log := fakeTunnelsAPI(t, map[string]func(http.ResponseWriter){
				"POST /v1/tunnels/tnl_01ABC/certificates/tcrt_01/archive": respond(tc.status, tc.body),
			})
			r := &TunnelCertificateResource{client: oauthtest.NewClient(t, srv)}

			st := resourceState(t, r, map[string]string{"id": "tcrt_01", "tunnel_id": "tnl_01ABC"})
			resp := &resource.DeleteResponse{State: st}
			r.Delete(context.Background(), resource.DeleteRequest{State: st}, resp)
			if got := resp.Diagnostics.HasError(); got != tc.wantErr {
				t.Errorf("error = %v, want %v (%v)", got, tc.wantErr, resp.Diagnostics)
			}
			if n := log.count(http.MethodPost, "/v1/tunnels/tnl_01ABC/certificates/tcrt_01/archive"); n != 1 {
				t.Errorf("archive called %d times, want 1", n)
			}
		})
	}
}

func TestTunnelCertificateResource_ImportState(t *testing.T) {
	r := &TunnelCertificateResource{}

	resp := &resource.ImportStateResponse{State: resourceState(t, r, nil)}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: "tnl_01ABC:tcrt_01"}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("ImportState returned errors: %v", resp.Diagnostics)
	}
	var data TunnelCertificateResourceModel
	resp.State.Get(context.Background(), &data)
	if data.TunnelID.ValueString() != "tnl_01ABC" || data.ID.ValueString() != "tcrt_01" {
		t.Errorf("tunnel_id/id = %v/%v", data.TunnelID, data.ID)
	}

	for _, bad := range []string{"tcrt_01", ":tcrt_01", "tnl_01ABC:", ""} {
		resp := &resource.ImportStateResponse{State: resourceState(t, r, nil)}
		r.ImportState(context.Background(), resource.ImportStateRequest{ID: bad}, resp)
		if !resp.Diagnostics.HasError() {
			t.Errorf("import ID %q: expected an error", bad)
		}
	}
}
