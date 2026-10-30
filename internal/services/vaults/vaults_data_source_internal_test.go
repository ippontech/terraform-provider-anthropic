// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package vaults

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/ippontech/terraform-provider-anthropic/internal/oauthtest"
)

func vaultListItemJSON(id string) string {
	return `{"id":"` + id + `","type":"vault","display_name":"` + id + `","metadata":{},` +
		`"created_at":"2026-09-01T10:00:00Z","updated_at":"2026-09-01T10:00:00Z","archived_at":null}`
}

// TestVaultsDataSourceRead_paginatesAndForwardsIncludeArchived drives Read
// against a two-page server: both pages are fetched in order, include_archived
// = true is forwarded and the second request carries the opaque cursor.
func TestVaultsDataSourceRead_paginatesAndForwardsIncludeArchived(t *testing.T) {
	var gotQueries []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/vaults" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		requireManagedAgentsBeta(t, r)
		gotQueries = append(gotQueries, r.URL.Query())
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "" {
			_, _ = io.WriteString(w, `{"data":[`+vaultListItemJSON("vlt_01")+`,`+vaultJSON+`],"next_page":"cursor-2"}`)
			return
		}
		_, _ = io.WriteString(w, `{"data":[`+vaultListItemJSON("vlt_03")+`],"next_page":null}`)
	}))
	defer srv.Close()

	resp := readDataSource(t, &VaultsDataSource{client: oauthtest.NewSDKClient(t, srv, option.WithMaxRetries(0))}, map[string]tftypes.Value{
		"include_archived": tftypes.NewValue(tftypes.Bool, true),
	})
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}

	var got VaultsDataSourceModel
	resp.Diagnostics.Append(resp.State.Get(context.Background(), &got)...)
	vaults := got.Vaults.Elements()
	if len(vaults) != 3 {
		t.Fatalf("expected 3 vaults across 2 pages, got %d", len(vaults))
	}
	ids := make([]string, len(vaults))
	for i, v := range vaults {
		ids[i] = v.(types.Object).Attributes()["id"].(types.String).ValueString()
	}
	if ids[0] != "vlt_01" || ids[1] != "vlt_01ABC" || ids[2] != "vlt_03" {
		t.Errorf("ids = %v, want the API order [vlt_01 vlt_01ABC vlt_03]", ids)
	}
	archived := vaults[1].(types.Object).Attributes()
	if got := archived["archived_at"].(types.String).ValueString(); got != "2026-09-03T12:00:00Z" {
		t.Errorf("archived_at = %q, want 2026-09-03T12:00:00Z", got)
	}
	if !vaults[0].(types.Object).Attributes()["metadata"].IsNull() {
		t.Error("metadata = {} from the API should map to null")
	}

	if len(gotQueries) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(gotQueries))
	}
	if got := gotQueries[0].Get("include_archived"); got != "true" {
		t.Errorf("include_archived = %q, want true", got)
	}
	if got := gotQueries[1].Get("page"); got != "cursor-2" {
		t.Errorf("second request page = %q, want cursor-2", got)
	}
}

// TestVaultsDataSourceRead_defaultOmitsIncludeArchived pins the opposite
// direction: null and false both leave include_archived out (false is the API
// default), and an empty result is an empty list, never null.
func TestVaultsDataSourceRead_defaultOmitsIncludeArchived(t *testing.T) {
	for name, includeArchived := range map[string]tftypes.Value{
		"null":  tftypes.NewValue(tftypes.Bool, nil),
		"false": tftypes.NewValue(tftypes.Bool, false),
	} {
		t.Run(name, func(t *testing.T) {
			var gotQuery url.Values
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotQuery = r.URL.Query()
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"data":[],"next_page":null}`)
			}))
			defer srv.Close()

			resp := readDataSource(t, &VaultsDataSource{client: oauthtest.NewSDKClient(t, srv)}, map[string]tftypes.Value{
				"include_archived": includeArchived,
			})
			if resp.Diagnostics.HasError() {
				t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
			}
			if _, present := gotQuery["include_archived"]; present {
				t.Errorf("include_archived sent as %q, want omitted", gotQuery.Get("include_archived"))
			}

			var got VaultsDataSourceModel
			resp.Diagnostics.Append(resp.State.Get(context.Background(), &got)...)
			if got.Vaults.IsNull() || len(got.Vaults.Elements()) != 0 {
				t.Errorf("vaults = %v, want an empty, non-null list", got.Vaults)
			}
		})
	}
}

func TestVaultsDataSourceRead_apiError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"permission_error","message":"forbidden"}}`)
	}))
	defer srv.Close()

	resp := readDataSource(t, &VaultsDataSource{client: oauthtest.NewSDKClient(t, srv)}, nil)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error diagnostic for a failed list")
	}
}
