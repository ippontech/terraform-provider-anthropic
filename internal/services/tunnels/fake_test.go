// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package tunnels_test

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// Routes of the Tunnels API (research preview, beta mcp-tunnels-2026-06-22,
// ?beta=true, OAuth bearer with workspace:manage_tunnels). References, under
// https://platform.claude.com/docs/en/api/beta/tunnels/: create, retrieve,
// list, archive, reveal_token, rotate_token, and certificates/create,
// retrieve, archive.
const (
	routeCreateTunnel  = "POST /v1/tunnels"
	routeListTunnels   = "GET /v1/tunnels"
	routeGetTunnel     = "GET /v1/tunnels/{tunnel_id}"
	routeArchiveTunnel = "POST /v1/tunnels/{tunnel_id}/archive"
	routeRevealToken   = "POST /v1/tunnels/{tunnel_id}/reveal_token"
	routeRotateToken   = "POST /v1/tunnels/{tunnel_id}/rotate_token"
	routeCreateCert    = "POST /v1/tunnels/{tunnel_id}/certificates"
	routeGetCert       = "GET /v1/tunnels/{tunnel_id}/certificates/{certificate_id}"
	routeArchiveCert   = "POST /v1/tunnels/{tunnel_id}/certificates/{certificate_id}/archive"

	tunnelsBeta = "mcp-tunnels-2026-06-22"
)

var allRoutes = []string{
	routeCreateTunnel, routeListTunnels, routeGetTunnel, routeArchiveTunnel, routeRevealToken, routeRotateToken,
	routeCreateCert, routeGetCert, routeArchiveCert,
}

// tunnelsFake is the Tunnels API on two sdktest collections, plus each
// tunnel's current token, which only reveal and rotate show.
type tunnelsFake struct {
	*sdktest.Server
	tunnels *sdktest.Collection
	certs   *sdktest.Collection

	mu        sync.Mutex
	tokens    map[string]int // tunnel ID -> token generation
	lastToken int
}

func newTunnelsFake(t *testing.T, pageSize int) *tunnelsFake {
	t.Helper()
	srv := sdktest.NewServer(t)
	f := &tunnelsFake{Server: srv, tunnels: srv.NewCollection("tnl_"), certs: srv.NewCollection("tcrt_"), tokens: map[string]int{}}
	f.Handle(routeCreateTunnel, f.createTunnel)
	f.Handle(routeListTunnels, func(w http.ResponseWriter, r *http.Request) {
		archived := r.URL.Query().Get("include_archived") == "true"
		items := f.tunnels.List(func(tn map[string]any) bool { return archived || tn["archived_at"] == nil })
		slices.Reverse(items) // newest first
		f.Paginate(w, r, sdktest.PageToken, pageSize, items)
	})
	f.tunnels.HandleGet(routeGetTunnel)
	f.Handle(routeArchiveTunnel, f.archiveTunnel)
	f.Handle(routeRevealToken, func(w http.ResponseWriter, r *http.Request) { f.token(w, r, false) })
	f.Handle(routeRotateToken, func(w http.ResponseWriter, r *http.Request) { f.token(w, r, true) })
	f.Handle(routeCreateCert, f.createCert)
	f.certs.HandleGet(routeGetCert)
	f.certs.HandleArchive(routeArchiveCert)
	return f
}

// newTunnel stores a live tunnel with a fresh domain and token (create
// reference: "allocates a fresh hostname"), which mapTunnelToState and
// mapTunnelTokenToState map.
func (f *tunnelsFake) newTunnel(displayName any) map[string]any {
	tn := f.tunnels.Create(map[string]any{"type": "tunnel", "display_name": displayName, "archived_at": nil})
	tn["domain"] = domain(tn["id"].(string))
	f.tunnels.Put(tn)
	f.mu.Lock()
	f.lastToken++
	f.tokens[tn["id"].(string)] = f.lastToken
	f.mu.Unlock()
	return tn
}

func (f *tunnelsFake) createTunnel(w http.ResponseWriter, r *http.Request) {
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	sdktest.WriteJSON(w, http.StatusOK, f.newTunnel(body["display_name"]))
}

// archiveTunnel archives the tunnel and its live certificates in the same
// operation, and is idempotent (archive reference). The provider only
// documents the cascade (TunnelResource's description); checkAllArchived
// relies on it for certificates registered outside Terraform.
func (f *tunnelsFake) archiveTunnel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("tunnel_id")
	tn, ok := f.tunnels.Archive(id)
	if !ok {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "tunnel "+id+" not found")
		return
	}
	for _, c := range f.certs.List(func(c map[string]any) bool { return c["tunnel_id"] == id }) {
		f.certs.Archive(c["id"].(string))
	}
	sdktest.WriteJSON(w, http.StatusOK, tn)
}

// token answers the tunnel's current token, the same until a rotation, which
// replaces it (reveal_token and rotate_token references): mapTunnelTokenToState
// maps it, and TunnelResource.Update rotates it when token_rotation changes.
func (f *tunnelsFake) token(w http.ResponseWriter, r *http.Request, rotate bool) {
	id := r.PathValue("tunnel_id")
	if !f.tunnels.Has(id) {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "tunnel "+id+" not found")
		return
	}
	f.mu.Lock()
	if rotate {
		f.lastToken++
		f.tokens[id] = f.lastToken
	}
	n := f.tokens[id]
	f.mu.Unlock()
	sdktest.WriteJSON(w, http.StatusOK, map[string]any{"type": "tunnel_token", "id": tokenID(n), "tunnel_token": tokenValue(n)})
}

// domain is the hostname the fake allocates to the tunnel id.
func domain(id string) string { return strings.ReplaceAll(id, "_", "-") + ".tunnels.sdktest.invalid" }

// tokenID and tokenValue are the n-th token the fake hands out.
func tokenID(n int) string    { return fmt.Sprintf("ttok_%02d", n) }
func tokenValue(n int) string { return fmt.Sprintf("sdktest-tunnel-token-%02d", n) }

// createCert registers a CA on a tunnel, reporting the "Lowercase hex
// SHA-256 fingerprint of the certificate's DER encoding" and its expiry
// (https://platform.claude.com/docs/en/api/beta/tunnels/certificates/create).
// caCertificateChanged compares that fingerprint with the configured PEM's
// to update a re-encoded certificate in place, and
// mapTunnelCertificateToState maps both. The provider's validator refuses a
// PEM that is not one certificate before any request.
func (f *tunnelsFake) createCert(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("tunnel_id")
	if !f.tunnels.Has(id) {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "tunnel "+id+" not found")
		return
	}
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	block, _ := pem.Decode([]byte(fmt.Sprint(body["ca_certificate_pem"])))
	if block == nil {
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", "ca_certificate_pem is not PEM")
		return
	}
	var expiresAt any
	if cert, err := x509.ParseCertificate(block.Bytes); err == nil {
		expiresAt = cert.NotAfter.UTC().Format(time.RFC3339)
	}
	sum := sha256.Sum256(block.Bytes)
	sdktest.WriteJSON(w, http.StatusOK, f.certs.Create(map[string]any{
		"type": "tunnel_certificate", "tunnel_id": id, "fingerprint": hex.EncodeToString(sum[:]),
		"expires_at": expiresAt, "archived_at": nil,
	}))
}

// assertRequestShape checks what every request of a test must carry: the
// tunnels beta, the OAuth bearer and ?beta=true, on every route called.
func (f *tunnelsFake) assertRequestShape(t *testing.T) {
	t.Helper()
	f.AssertShape(sdktest.Shape{Credential: sdktest.AuthToken, Beta: tunnelsBeta, BetaQuery: true}, allRoutes...)
}

// requestPaths returns the path and fault of every request to route.
func (f *tunnelsFake) requestPaths(route string) []string {
	var out []string
	for _, r := range f.Requests(route) {
		out = append(out, r.Path+" "+r.Fault)
	}
	return out
}
