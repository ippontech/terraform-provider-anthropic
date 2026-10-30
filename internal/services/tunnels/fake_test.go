// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package tunnels_test

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
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
// reference: "allocates a fresh hostname").
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
	body, ok := decodeBody(w, r)
	if !ok {
		return
	}
	sdktest.WriteJSON(w, http.StatusOK, f.newTunnel(body["display_name"]))
}

// archiveTunnel archives the tunnel and its live certificates in the same
// operation, and is idempotent (archive reference).
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
// replaces it (reveal_token and rotate_token references). An archived tunnel
// has no token.
func (f *tunnelsFake) token(w http.ResponseWriter, r *http.Request, rotate bool) {
	id := r.PathValue("tunnel_id")
	tn, ok := f.tunnels.Get(id)
	switch {
	case !ok:
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "tunnel "+id+" not found")
		return
	case tn["archived_at"] != nil:
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", "tunnel "+id+" is archived")
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

// createCert registers a CA on a live tunnel, at most two live ones per
// tunnel, reporting the DER fingerprint and the expiry (certificates/create
// reference). The API's answer to a third certificate is not documented; the
// fake gives a 409 conflict_error.
func (f *tunnelsFake) createCert(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("tunnel_id")
	tn, ok := f.tunnels.Get(id)
	if !ok {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "tunnel "+id+" not found")
		return
	}
	body, ok := decodeBody(w, r)
	if !ok {
		return
	}
	block, _ := pem.Decode([]byte(fmt.Sprint(body["ca_certificate_pem"])))
	var cert *x509.Certificate
	if block != nil {
		cert, _ = x509.ParseCertificate(block.Bytes)
	}
	live := f.certs.List(func(c map[string]any) bool { return c["tunnel_id"] == id && c["archived_at"] == nil })
	switch {
	case tn["archived_at"] != nil:
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", "tunnel "+id+" is archived")
	case cert == nil:
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", "ca_certificate_pem is not a certificate")
	case len(live) >= 2:
		sdktest.WriteAPIError(w, http.StatusConflict, "", "tunnel "+id+" already has two certificates")
	default:
		sum := sha256.Sum256(block.Bytes)
		sdktest.WriteJSON(w, http.StatusOK, f.certs.Create(map[string]any{
			"type": "tunnel_certificate", "tunnel_id": id, "fingerprint": hex.EncodeToString(sum[:]),
			"expires_at": cert.NotAfter.UTC().Format(time.RFC3339), "archived_at": nil,
		}))
	}
}

// assertRequestShape checks what every request of a test must carry: the
// tunnels beta, the OAuth bearer and ?beta=true, on every route called.
func (f *tunnelsFake) assertRequestShape(t *testing.T) {
	t.Helper()
	for _, route := range allRoutes {
		if len(f.Requests(route)) == 0 {
			continue
		}
		f.AssertBeta(route, tunnelsBeta)
		f.AssertCredential(route, sdktest.AuthToken)
		for _, r := range f.Requests(route) {
			if r.Query.Get("beta") != "true" {
				t.Errorf("%s %s without ?beta=true", r.Method, r.Path)
			}
		}
	}
}

// requestPaths returns the path and fault of every request to route.
func (f *tunnelsFake) requestPaths(route string) []string {
	var out []string
	for _, r := range f.Requests(route) {
		out = append(out, r.Path+" "+r.Fault)
	}
	return out
}

// decodeBody reads a JSON object body (an empty body is {}) or answers 400
// invalid_request_error.
func decodeBody(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	body := map[string]any{}
	if r.ContentLength == 0 {
		return body, true
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", "body is not a JSON object: "+err.Error())
		return nil, false
	}
	return body, true
}
