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

func TestExternalKeysDataSource_Read_emptyAndRepeatedCursor(t *testing.T) {
	calls := 0
	srv := newKeyServer(t, func(w http.ResponseWriter, _ *http.Request, _ map[string]any) {
		calls++
		_, _ = io.WriteString(w, `{"data":[],"next_page":"same"}`)
	})

	resp := readDataSource(t, &ExternalKeysDataSource{client: admintest.NewClient(t, srv)}, nil)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", resp.Diagnostics)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2 (stop when the cursor repeats)", calls)
	}

	var data ExternalKeysDataSourceModel
	if diags := resp.State.Get(context.Background(), &data); diags.HasError() {
		t.Fatalf("state: %v", diags)
	}
	if data.ExternalKeys.IsNull() || len(data.ExternalKeys.Elements()) != 0 {
		t.Errorf("external_keys = %v, want an empty, non-null list", data.ExternalKeys)
	}
}
