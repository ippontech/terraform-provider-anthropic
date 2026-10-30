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

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/ippontech/terraform-provider-anthropic/internal/oauthtest"
)

func readTunnels(t *testing.T, srv *httptest.Server, overrides map[string]tftypes.Value) (tunnelsDSModel, bool) {
	t.Helper()
	resp := readDataSource(t, &TunnelsDataSource{client: oauthtest.NewClient(t, srv)}, overrides)
	var data tunnelsDSModel
	if !resp.Diagnostics.HasError() {
		resp.Diagnostics.Append(resp.State.Get(context.Background(), &data)...)
	}
	if resp.Diagnostics.HasError() {
		t.Logf("diagnostics: %v", resp.Diagnostics)
	}
	return data, resp.Diagnostics.HasError()
}

// TestTunnelsDataSource_Read_paginates exercises Read against a fake
// two-page server: both pages are fetched, include_archived and the page size
// reach the first request, and the cursor drives the second.
func TestTunnelsDataSource_Read_paginates(t *testing.T) {
	var gotQueries []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/tunnels" {
			t.Errorf("path = %s, want /v1/tunnels", r.URL.Path)
		}
		gotQueries = append(gotQueries, r.URL.Query())
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "" {
			_, _ = io.WriteString(w, `{"data":[`+tunnelLiveJSON+`],"next_page":"cursor-2"}`)
			return
		}
		archived := strings.Replace(tunnelArchivedJSON, "tnl_01ABC", "tnl_00OLD", 1)
		_, _ = io.WriteString(w, `{"data":[`+archived+`],"next_page":null}`)
	}))
	defer srv.Close()

	data, hasErr := readTunnels(t, srv, map[string]tftypes.Value{
		"include_archived": tftypes.NewValue(tftypes.Bool, true),
	})
	if hasErr {
		t.Fatal("unexpected diagnostics")
	}

	tunnels := data.Tunnels.Elements()
	if len(tunnels) != 2 {
		t.Fatalf("expected 2 tunnels across 2 pages, got %d", len(tunnels))
	}
	first := tunnels[0].(types.Object).Attributes()
	if first["id"].(types.String).ValueString() != "tnl_01ABC" || !first["archived_at"].(types.String).IsNull() {
		t.Errorf("tunnels[0] = %v, want the live tnl_01ABC", first)
	}
	second := tunnels[1].(types.Object).Attributes()
	if second["archived_at"].(types.String).ValueString() != "2026-07-02T10:00:00Z" {
		t.Errorf("tunnels[1].archived_at = %v", second["archived_at"])
	}

	if len(gotQueries) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(gotQueries))
	}
	if got := gotQueries[0].Get("include_archived"); got != "true" {
		t.Errorf("include_archived = %q, want true", got)
	}
	if got := gotQueries[0].Get("limit"); got != "100" {
		t.Errorf("limit = %q, want 100", got)
	}
	if got := gotQueries[1].Get("page"); got != "cursor-2" {
		t.Errorf("second request page = %q, want cursor-2", got)
	}
}

// TestTunnelsDataSource_Read_defaultOmitsIncludeArchived: left unset, the
// filter is not sent and the API's own default (exclude archived) applies;
// an empty result is an empty list, not null.
func TestTunnelsDataSource_Read_defaultOmitsIncludeArchived(t *testing.T) {
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[],"next_page":null}`)
	}))
	defer srv.Close()

	data, hasErr := readTunnels(t, srv, nil)
	if hasErr {
		t.Fatal("unexpected diagnostics")
	}
	if _, present := gotQuery["include_archived"]; present {
		t.Errorf("include_archived sent as %q, want omitted", gotQuery.Get("include_archived"))
	}
	if data.Tunnels.IsNull() || len(data.Tunnels.Elements()) != 0 {
		t.Errorf("tunnels = %v, want an empty list", data.Tunnels)
	}
}

func TestTunnelsDataSource_Read_error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"permission_error","message":"scope workspace:manage_tunnels required"}}`)
	}))
	defer srv.Close()

	if _, hasErr := readTunnels(t, srv, nil); !hasErr {
		t.Fatal("expected diagnostics for a 403 response")
	}
}
