// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package externalkeys

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"

	"github.com/ippontech/terraform-provider-anthropic/internal/admintest"
)

// --- anthropic_external_keys ---

func TestExternalKeysDataSource_Read_paginatesWithPageCursor(t *testing.T) {
	var mu sync.Mutex
	var queries []url.Values
	srv := newKeyServer(t, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
		mu.Lock()
		queries = append(queries, r.URL.Query())
		mu.Unlock()
		if r.URL.Path != "/v1/organizations/external_keys" {
			http.Error(w, "unexpected path "+r.URL.Path, http.StatusBadRequest)
			return
		}
		if r.URL.Query().Get("page") == "" {
			_, _ = io.WriteString(w, `{"data":[`+awsKeyFixture("attached")+`],"next_page":"cursor_2"}`)
			return
		}
		_, _ = io.WriteString(w, `{"data":[`+azureKeyFixture+`],"next_page":null}`)
	})

	resp := readDataSource(t, &ExternalKeysDataSource{client: admintest.NewClient(t, srv)}, nil)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", resp.Diagnostics)
	}

	if len(queries) != 2 {
		t.Fatalf("requests = %d, want 2", len(queries))
	}
	if queries[0].Get("limit") != "100" || queries[0].Has("page") || queries[0].Has("beta") {
		t.Errorf("first query = %v, want limit=100 only", queries[0])
	}
	if queries[1].Get("page") != "cursor_2" || queries[1].Get("limit") != "100" {
		t.Errorf("second query = %v, want page=cursor_2&limit=100", queries[1])
	}

	var data ExternalKeysDataSourceModel
	if diags := resp.State.Get(context.Background(), &data); diags.HasError() {
		t.Fatalf("state: %v", diags)
	}
	keys := data.ExternalKeys.Elements()
	if len(keys) != 2 {
		t.Fatalf("external_keys = %d, want 2", len(keys))
	}
	if !strings.Contains(keys[1].String(), `"ekey_01AZURE"`) {
		t.Errorf("second key = %s, want ekey_01AZURE in API order", keys[1])
	}
}

func TestExternalKeysDataSource_Read_empty(t *testing.T) {
	calls := 0
	srv := newKeyServer(t, func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
		calls++
		_, _ = io.WriteString(w, `{"data":[],"next_page":null}`)
	})

	resp := readDataSource(t, &ExternalKeysDataSource{client: admintest.NewClient(t, srv)}, nil)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", resp.Diagnostics)
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1", calls)
	}

	var data ExternalKeysDataSourceModel
	if diags := resp.State.Get(context.Background(), &data); diags.HasError() {
		t.Fatalf("state: %v", diags)
	}
	if data.ExternalKeys.IsNull() || len(data.ExternalKeys.Elements()) != 0 {
		t.Errorf("external_keys = %v, want an empty, non-null list", data.ExternalKeys)
	}
}

// A next_page already sent, the one just sent (an API returning the same
// cursor forever) or an earlier one (a cycle), fails the read instead of
// looping. The fake answers a null cursor after 20 requests, so without the
// guard the read ends, with no error, and the test fails rather than hangs.
func TestExternalKeysDataSource_Read_repeatedCursorFails(t *testing.T) {
	cases := []struct {
		name     string
		next     map[string]string // page sent -> next_page returned
		requests int
	}{
		{name: "same cursor forever", next: map[string]string{"": "same", "same": "same"}, requests: 2},
		{name: "cycle", next: map[string]string{"": "cursor_2", "cursor_2": "cursor_3", "cursor_3": "cursor_2"}, requests: 3},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			requests := 0
			srv := newKeyServer(t, func(w http.ResponseWriter, r *http.Request, _ map[string]any) {
				requests++
				next := `"` + tc.next[r.URL.Query().Get("page")] + `"`
				if requests >= 20 {
					next = "null"
				}
				_, _ = io.WriteString(w, `{"data":[`+azureKeyFixture+`],"next_page":`+next+`}`)
			})

			resp := readDataSource(t, &ExternalKeysDataSource{client: admintest.NewClient(t, srv)}, nil)
			if !resp.Diagnostics.HasError() {
				t.Fatalf("Read succeeded after %d requests, want a repeated cursor error", requests)
			}
			if got := resp.Diagnostics.Errors()[0].Detail(); !strings.Contains(got, "a cursor already sent") {
				t.Errorf("error = %q, want a repeated cursor error", got)
			}
			if requests != tc.requests {
				t.Errorf("requests = %d, want %d", requests, tc.requests)
			}
		})
	}
}
