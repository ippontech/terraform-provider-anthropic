// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package apikeys

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func makeAPIKeyListPage(keys []apiKeyAPIResponse, hasMore bool, lastID string) string {
	data, _ := json.Marshal(keys)
	return fmt.Sprintf(`{"data":%s,"has_more":%v,"first_id":"","last_id":%q}`, data, hasMore, lastID)
}

func TestAPIKeysDataSource_listAll(t *testing.T) {
	wsID := "wrkspc_01WS"
	keys := []apiKeyAPIResponse{
		{ID: "apikey_01A", Name: "Key A", Status: "active", WorkspaceID: &wsID, CreatedAt: "2026-01-01T00:00:00Z", CreatedBy: apiKeyCreatedBy{ID: "user_01X", Type: "user"}, PartialKeyHint: "aaaa", Type: "api_key"},
		{ID: "apikey_01B", Name: "Key B", Status: "inactive", WorkspaceID: nil, CreatedAt: "2026-02-01T00:00:00Z", CreatedBy: apiKeyCreatedBy{ID: "user_01Y", Type: "user"}, PartialKeyHint: "bbbb", Type: "api_key"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/organizations/api_keys" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, makeAPIKeyListPage(keys, false, ""))
	}))
	defer srv.Close()

	client := newTestAdminClient(t, srv)
	body, err := client.DoRequest(context.Background(), "GET", "/v1/organizations/api_keys?limit=1000", nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	var page apiKeyListAPIResponse
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("parse: %v", err)
	}

	if len(page.Data) != 2 {
		t.Errorf("len(Data) = %d, want 2", len(page.Data))
	}
	if page.HasMore {
		t.Error("HasMore should be false")
	}
}

func TestAPIKeysDataSource_pagination(t *testing.T) {
	wsID := "wrkspc_01WS"
	page1 := []apiKeyAPIResponse{
		{ID: "apikey_01A", Name: "Key A", Status: "active", WorkspaceID: &wsID, CreatedBy: apiKeyCreatedBy{ID: "u", Type: "user"}, Type: "api_key"},
	}
	page2 := []apiKeyAPIResponse{
		{ID: "apikey_01B", Name: "Key B", Status: "active", WorkspaceID: &wsID, CreatedBy: apiKeyCreatedBy{ID: "u", Type: "user"}, Type: "api_key"},
	}

	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		q := r.URL.Query()
		w.WriteHeader(http.StatusOK)
		if q.Get("after_id") == "" {
			_, _ = io.WriteString(w, makeAPIKeyListPage(page1, true, "apikey_01A"))
		} else {
			_, _ = io.WriteString(w, makeAPIKeyListPage(page2, false, ""))
		}
	}))
	defer srv.Close()

	client := newTestAdminClient(t, srv)

	var all []apiKeyAPIResponse
	afterID := ""
	for {
		params := url.Values{}
		params.Set("limit", "1000")
		if afterID != "" {
			params.Set("after_id", afterID)
		}
		body, err := client.DoRequest(context.Background(), "GET", "/v1/organizations/api_keys?"+params.Encode(), nil)
		if err != nil {
			t.Fatalf("page request: %v", err)
		}
		var page apiKeyListAPIResponse
		if err := json.Unmarshal(body, &page); err != nil {
			t.Fatalf("parse: %v", err)
		}
		all = append(all, page.Data...)
		if !page.HasMore {
			break
		}
		afterID = page.LastID
	}

	if len(all) != 2 {
		t.Errorf("total keys = %d, want 2", len(all))
	}
	if callCount != 2 {
		t.Errorf("callCount = %d, want 2", callCount)
	}
}

func TestAPIKeysDataSource_filtersPassedAsQueryParams(t *testing.T) {
	var capturedQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.Query()
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, makeAPIKeyListPage(nil, false, ""))
	}))
	defer srv.Close()

	client := newTestAdminClient(t, srv)
	_, _ = client.DoRequest(context.Background(), "GET",
		"/v1/organizations/api_keys?limit=1000&status=active&workspace_id=wrkspc_01WS", nil)

	if capturedQuery.Get("status") != "active" {
		t.Errorf("status param = %q, want %q", capturedQuery.Get("status"), "active")
	}
	if capturedQuery.Get("workspace_id") != "wrkspc_01WS" {
		t.Errorf("workspace_id param = %q, want %q", capturedQuery.Get("workspace_id"), "wrkspc_01WS")
	}
}

func TestMapAPIKeyToListObject_withWorkspaceID(t *testing.T) {
	wsID := "wrkspc_01WS"
	key := &apiKeyAPIResponse{
		ID:             "apikey_01ABC",
		Name:           "Test Key",
		Status:         "active",
		WorkspaceID:    &wsID,
		CreatedAt:      "2026-01-01T00:00:00Z",
		CreatedBy:      apiKeyCreatedBy{ID: "user_01X", Type: "user"},
		PartialKeyHint: "abcd",
		Type:           "api_key",
	}

	obj, diags := mapAPIKeyToListObject(key)
	if diags.HasError() {
		t.Fatalf("mapAPIKeyToListObject error: %v", diags)
	}
	if obj == nil {
		t.Fatal("expected non-nil object")
	}
}

func TestMapAPIKeyToListObject_nullWorkspaceID(t *testing.T) {
	key := &apiKeyAPIResponse{
		ID:             "apikey_01ABC",
		Name:           "Org Key",
		Status:         "active",
		WorkspaceID:    nil,
		CreatedAt:      "2026-01-01T00:00:00Z",
		CreatedBy:      apiKeyCreatedBy{ID: "user_01X", Type: "user"},
		PartialKeyHint: "abcd",
		Type:           "api_key",
	}

	obj, diags := mapAPIKeyToListObject(key)
	if diags.HasError() {
		t.Fatalf("mapAPIKeyToListObject error: %v", diags)
	}
	if obj == nil {
		t.Fatal("expected non-nil object")
	}

	// Cast to types.Object to inspect workspace_id
	typedObj, ok := obj.(types.Object)
	if !ok {
		t.Fatalf("expected types.Object, got %T", obj)
	}
	wsIDVal, exists := typedObj.Attributes()["workspace_id"]
	if !exists {
		t.Fatal("workspace_id attribute missing")
	}
	wsIDStr, ok := wsIDVal.(types.String)
	if !ok {
		t.Fatalf("workspace_id is not types.String, got %T", wsIDVal)
	}
	if !wsIDStr.IsNull() {
		t.Errorf("workspace_id should be null, got %q", wsIDStr.ValueString())
	}
}
