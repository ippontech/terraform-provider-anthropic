// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package tunnels

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/ippontech/terraform-provider-anthropic/internal/oauthtest"
)

const certificatesPath = "/v1/tunnels/tnl_01ABC/certificates"

// fakeCertificatesAPI serves GET /v1/tunnels/tnl_01ABC/certificates with the
// status and body pageFor returns for the `page` query value ("" for the first
// request), recording every request.
func fakeCertificatesAPI(t *testing.T, pageFor func(page string) (int, string)) (*httptest.Server, *requestLog) {
	t.Helper()
	log := &requestLog{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.add(r, "")
		w.Header().Set("Content-Type", "application/json")
		if r.Method != http.MethodGet || r.URL.Path != certificatesPath {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, notFoundBody)
			return
		}
		status, body := pageFor(r.URL.Query().Get("page"))
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, log
}

// readTunnelCertificates reads the data source for tunnel tnl_01ABC, with
// the given config overrides.
func readTunnelCertificates(t *testing.T, srv *httptest.Server, overrides map[string]tftypes.Value) (tunnelCertificatesDSModel, bool) {
	t.Helper()
	config := map[string]tftypes.Value{"tunnel_id": tftypes.NewValue(tftypes.String, "tnl_01ABC")}
	for k, v := range overrides {
		config[k] = v
	}
	resp := readDataSource(t, &TunnelCertificatesDataSource{client: oauthtest.NewClient(t, srv)}, config)
	var data tunnelCertificatesDSModel
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Get(context.Background(), &data)...)
	}
	if resp.Diagnostics.HasError() {
		t.Logf("diagnostics: %v", resp.Diagnostics)
	}
	return data, resp.Diagnostics.HasError()
}

func certificateAttrs(t *testing.T, data tunnelCertificatesDSModel, i int) map[string]string {
	t.Helper()
	elems := data.Certificates.Elements()
	if i >= len(elems) {
		t.Fatalf("certificates has %d elements, want at least %d", len(elems), i+1)
	}
	attrs := map[string]string{}
	for name, v := range elems[i].(types.Object).Attributes() {
		s := v.(types.String)
		if s.IsNull() {
			attrs[name] = "<null>"
			continue
		}
		attrs[name] = s.ValueString()
	}
	return attrs
}

func assertCertificate(t *testing.T, label string, got, want map[string]string) {
	t.Helper()
	for name, w := range want {
		if got[name] != w {
			t.Errorf("%s.%s = %q, want %q", label, name, got[name], w)
		}
	}
}

// TestTunnelCertificatesDataSource_Read_paginates exercises Read against a
// fake two-page server: both pages are fetched with the tunnels beta header
// and the bearer token, include_archived and the page size reach the first
// request, the cursor drives the second, and every field is mapped, archived
// certificate included.
func TestTunnelCertificatesDataSource_Read_paginates(t *testing.T) {
	srv, log := fakeCertificatesAPI(t, func(page string) (int, string) {
		if page == "" {
			return http.StatusOK, `{"data":[` + certificateJSON("", "2031-07-01T10:00:00Z") + `],"next_page":"cursor-2"}`
		}
		archived := strings.Replace(certificateJSON("2026-07-02T10:00:00Z", ""), "tcrt_01", "tcrt_00", 1)
		return http.StatusOK, `{"data":[` + archived + `],"next_page":null}`
	})

	data, hasErr := readTunnelCertificates(t, srv, map[string]tftypes.Value{
		"include_archived": tftypes.NewValue(tftypes.Bool, true),
	})
	if hasErr {
		t.Fatal("unexpected diagnostics")
	}

	if n := len(data.Certificates.Elements()); n != 2 {
		t.Fatalf("expected 2 certificates across 2 pages, got %d", n)
	}
	assertCertificate(t, "certificates[0]", certificateAttrs(t, data, 0), map[string]string{
		"id":          "tcrt_01",
		"tunnel_id":   "tnl_01ABC",
		"fingerprint": "abcd",
		"created_at":  "2026-07-01T10:00:00Z",
		"expires_at":  "2031-07-01T10:00:00Z",
		"archived_at": "<null>",
	})
	assertCertificate(t, "certificates[1]", certificateAttrs(t, data, 1), map[string]string{
		"id":          "tcrt_00",
		"expires_at":  "<null>",
		"archived_at": "2026-07-02T10:00:00Z",
	})

	reqs := log.all()
	if len(reqs) != 2 {
		t.Fatalf("expected 2 requests, got %d: %+v", len(reqs), reqs)
	}
	for i, r := range reqs {
		if r.Path != certificatesPath || !strings.Contains(r.Beta, "mcp-tunnels-2026-06-22") || r.Auth != "Bearer test" {
			t.Errorf("request %d = %+v, want a GET on %s with the tunnels beta header and the bearer token", i, r, certificatesPath)
		}
	}
	firstQuery, _ := url.ParseQuery(reqs[0].Query)
	if got := firstQuery.Get("include_archived"); got != "true" {
		t.Errorf("include_archived = %q, want true", got)
	}
	if got := firstQuery.Get("limit"); got != "100" {
		t.Errorf("limit = %q, want 100", got)
	}
	secondQuery, _ := url.ParseQuery(reqs[1].Query)
	if got := secondQuery.Get("page"); got != "cursor-2" {
		t.Errorf("second request page = %q, want cursor-2", got)
	}
}

// TestTunnelCertificatesDataSource_Read_includeArchivedSentOnlyWhenTrue:
// unset or false, the filter is not sent and the API's own default (exclude
// archived) applies. An empty result is an empty list, not null, as for
// anthropic_tunnels.
func TestTunnelCertificatesDataSource_Read_includeArchivedSentOnlyWhenTrue(t *testing.T) {
	tests := map[string]struct {
		value tftypes.Value
		want  string // "" means omitted
	}{
		"unset": {tftypes.NewValue(tftypes.Bool, nil), ""},
		"false": {tftypes.NewValue(tftypes.Bool, false), ""},
		"true":  {tftypes.NewValue(tftypes.Bool, true), "true"},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			srv, log := fakeCertificatesAPI(t, func(string) (int, string) {
				return http.StatusOK, `{"data":[],"next_page":null}`
			})

			data, hasErr := readTunnelCertificates(t, srv, map[string]tftypes.Value{"include_archived": tc.value})
			if hasErr {
				t.Fatal("unexpected diagnostics")
			}
			reqs := log.all()
			if len(reqs) != 1 {
				t.Fatalf("expected 1 request, got %d", len(reqs))
			}
			query, _ := url.ParseQuery(reqs[0].Query)
			got, present := query["include_archived"]
			switch {
			case tc.want == "" && present:
				t.Errorf("include_archived sent as %q, want omitted", got)
			case tc.want != "" && query.Get("include_archived") != tc.want:
				t.Errorf("include_archived = %q, want %q", query.Get("include_archived"), tc.want)
			}
			if data.Certificates.IsNull() || len(data.Certificates.Elements()) != 0 {
				t.Errorf("certificates = %v, want an empty list", data.Certificates)
			}
		})
	}
}

// TestMapTunnelCertificateDSToListObject: a zero archived_at or expires_at,
// what the SDK decodes from null, an absent field or the zero time, maps to
// null; set timestamps map to RFC 3339.
func TestMapTunnelCertificateDSToListObject(t *testing.T) {
	created := time.Date(2026, 7, 1, 10, 0, 0, 0, time.UTC)
	tests := map[string]struct {
		cert anthropic.BetaTunnelCertificate
		want map[string]string
	}{
		"live, no expiry": {
			cert: anthropic.BetaTunnelCertificate{ID: "tcrt_01", TunnelID: "tnl_01ABC", Fingerprint: "abcd", CreatedAt: created},
			want: map[string]string{
				"id": "tcrt_01", "tunnel_id": "tnl_01ABC", "fingerprint": "abcd",
				"created_at": "2026-07-01T10:00:00Z", "expires_at": "<null>", "archived_at": "<null>",
			},
		},
		"archived, expiring": {
			cert: anthropic.BetaTunnelCertificate{
				ID: "tcrt_02", TunnelID: "tnl_01ABC", Fingerprint: "ef01", CreatedAt: created,
				ExpiresAt: created.AddDate(1, 0, 0), ArchivedAt: created.Add(24 * time.Hour),
			},
			want: map[string]string{
				"id": "tcrt_02", "expires_at": "2027-07-01T10:00:00Z", "archived_at": "2026-07-02T10:00:00Z",
			},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			obj, diags := mapTunnelCertificateDSToListObject(&tc.cert)
			if diags.HasError() {
				t.Fatalf("diagnostics: %v", diags)
			}
			list, diags := types.ListValue(types.ObjectType{AttrTypes: tunnelCertificateDSAttrTypes}, []attr.Value{obj})
			if diags.HasError() {
				t.Fatalf("list: %v", diags)
			}
			assertCertificate(t, name, certificateAttrs(t, tunnelCertificatesDSModel{Certificates: list}, 0), tc.want)
		})
	}
}

// TestTunnelCertificatesDataSource_Read_error: an API error, here an unknown
// tunnel, fails the read.
func TestTunnelCertificatesDataSource_Read_error(t *testing.T) {
	srv, _ := fakeCertificatesAPI(t, func(string) (int, string) {
		return http.StatusOK, `{"data":[],"next_page":null}`
	})

	resp := readDataSource(t, &TunnelCertificatesDataSource{client: oauthtest.NewClient(t, srv)}, map[string]tftypes.Value{
		"tunnel_id": tftypes.NewValue(tftypes.String, "tnl_missing"),
	})
	if !resp.Diagnostics.HasError() || !containsSummary(resp.Diagnostics, "Client Error") {
		t.Fatalf("diagnostics = %v, want a Client Error for a 404 response", resp.Diagnostics)
	}
	if detail := resp.Diagnostics.Errors()[0].Detail(); !strings.Contains(detail, "Unable to list tunnel certificates") {
		t.Errorf("detail = %q, want it to say the certificates could not be listed", detail)
	}
}

// TestTunnelCertificatesDataSource_Read_repeatedCursorFails: a next_page
// cursor the API already returned fails the read (sdkpage.Cursor) instead of
// looping until the plan's deadline.
func TestTunnelCertificatesDataSource_Read_repeatedCursorFails(t *testing.T) {
	srv, log := fakeCertificatesAPI(t, func(string) (int, string) {
		return http.StatusOK, `{"data":[],"next_page":"cursor-2"}`
	})

	if _, hasErr := readTunnelCertificates(t, srv, nil); !hasErr {
		t.Fatal("expected diagnostics for a repeated cursor")
	}
	if n := len(log.all()); n != 2 {
		t.Errorf("requests = %d, want 2 (the first page and the cursor's page)", n)
	}
}
