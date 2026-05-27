// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package apikeys

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
)

func newTestAdminClient(t *testing.T, srv *httptest.Server) *admin.Client {
	t.Helper()
	return &admin.Client{
		ApiKey:     "test-key",
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
	}
}

func apiKeyFixture(workspaceID *string) string {
	wsID := "null"
	if workspaceID != nil {
		wsID = `"` + *workspaceID + `"`
	}
	return `{
		"id": "apikey_01ABC",
		"name": "My API Key",
		"status": "active",
		"workspace_id": ` + wsID + `,
		"created_at": "2026-01-01T00:00:00Z",
		"created_by": {"id": "user_01XYZ", "type": "user"},
		"partial_key_hint": "abcd",
		"type": "api_key"
	}`
}

func TestAPIKeyRead_parsesResponse(t *testing.T) {
	wsID := "wrkspc_01WS"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/organizations/api_keys/apikey_01ABC" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, apiKeyFixture(&wsID))
	}))
	defer srv.Close()

	client := newTestAdminClient(t, srv)
	body, err := client.DoRequest(context.Background(), "GET", "/v1/organizations/api_keys/apikey_01ABC", nil)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var key apiKeyAPIResponse
	if err := json.Unmarshal(body, &key); err != nil {
		t.Fatalf("parse: %v", err)
	}

	if key.ID != "apikey_01ABC" {
		t.Errorf("ID = %q, want %q", key.ID, "apikey_01ABC")
	}
	if key.Name != "My API Key" {
		t.Errorf("Name = %q, want %q", key.Name, "My API Key")
	}
	if key.Status != "active" {
		t.Errorf("Status = %q, want %q", key.Status, "active")
	}
	if key.WorkspaceID == nil || *key.WorkspaceID != "wrkspc_01WS" {
		t.Errorf("WorkspaceID = %v, want %q", key.WorkspaceID, "wrkspc_01WS")
	}
	if key.CreatedBy.ID != "user_01XYZ" {
		t.Errorf("CreatedBy.ID = %q, want %q", key.CreatedBy.ID, "user_01XYZ")
	}
	if key.CreatedBy.Type != "user" {
		t.Errorf("CreatedBy.Type = %q, want %q", key.CreatedBy.Type, "user")
	}
	if key.PartialKeyHint != "abcd" {
		t.Errorf("PartialKeyHint = %q, want %q", key.PartialKeyHint, "abcd")
	}
	if key.Type != "api_key" {
		t.Errorf("Type = %q, want %q", key.Type, "api_key")
	}
}

func TestAPIKeyRead_nullWorkspaceID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, apiKeyFixture(nil))
	}))
	defer srv.Close()

	client := newTestAdminClient(t, srv)
	body, err := client.DoRequest(context.Background(), "GET", "/v1/organizations/api_keys/apikey_01ABC", nil)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var key apiKeyAPIResponse
	if err := json.Unmarshal(body, &key); err != nil {
		t.Fatalf("parse: %v", err)
	}

	if key.WorkspaceID != nil {
		t.Errorf("WorkspaceID = %v, want nil", key.WorkspaceID)
	}
}

func TestAPIKeyRead_notFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"type":"not_found_error","message":"api key not found"}}`)
	}))
	defer srv.Close()

	client := newTestAdminClient(t, srv)
	_, err := client.DoRequest(context.Background(), "GET", "/v1/organizations/api_keys/apikey_missing", nil)
	if !admin.IsNotFound(err) {
		t.Fatalf("expected IsNotFound, got: %v", err)
	}
}

func TestAPIKeyUpdate_sendsCorrectPayload(t *testing.T) {
	var capturedBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/organizations/api_keys/apikey_01ABC" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		capturedBody, _ = io.ReadAll(r.Body)
		wsID := "wrkspc_01WS"
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, apiKeyFixture(&wsID))
	}))
	defer srv.Close()

	client := newTestAdminClient(t, srv)
	updateReq := apiKeyUpdateRequest{Name: "Renamed Key", Status: "inactive"}
	_, err := client.DoRequest(context.Background(), "POST", "/v1/organizations/api_keys/apikey_01ABC", updateReq)
	if err != nil {
		t.Fatalf("update: %v", err)
	}

	var parsed map[string]string
	if err := json.Unmarshal(capturedBody, &parsed); err != nil {
		t.Fatalf("parse captured body: %v", err)
	}
	if parsed["name"] != "Renamed Key" {
		t.Errorf("name = %q, want %q", parsed["name"], "Renamed Key")
	}
	if parsed["status"] != "inactive" {
		t.Errorf("status = %q, want %q", parsed["status"], "inactive")
	}
}

func TestMapAPIKeyToState_setsAllFields(t *testing.T) {
	wsID := "wrkspc_01WS"
	key := &apiKeyAPIResponse{
		ID:             "apikey_01ABC",
		Name:           "My API Key",
		Status:         "active",
		WorkspaceID:    &wsID,
		CreatedAt:      "2026-01-01T00:00:00Z",
		CreatedBy:      apiKeyCreatedBy{ID: "user_01XYZ", Type: "user"},
		PartialKeyHint: "abcd",
		Type:           "api_key",
	}

	var data APIKeyResourceModel
	diags := mapAPIKeyToState(context.Background(), key, &data)
	if diags.HasError() {
		t.Fatalf("mapAPIKeyToState error: %v", diags)
	}

	if data.ID.ValueString() != "apikey_01ABC" {
		t.Errorf("ID = %q, want %q", data.ID.ValueString(), "apikey_01ABC")
	}
	if data.Name.ValueString() != "My API Key" {
		t.Errorf("Name = %q, want %q", data.Name.ValueString(), "My API Key")
	}
	if data.Status.ValueString() != "active" {
		t.Errorf("Status = %q, want %q", data.Status.ValueString(), "active")
	}
	if data.WorkspaceID.ValueString() != "wrkspc_01WS" {
		t.Errorf("WorkspaceID = %q, want %q", data.WorkspaceID.ValueString(), "wrkspc_01WS")
	}
	if data.PartialKeyHint.ValueString() != "abcd" {
		t.Errorf("PartialKeyHint = %q, want %q", data.PartialKeyHint.ValueString(), "abcd")
	}
	if data.Type.ValueString() != "api_key" {
		t.Errorf("Type = %q, want %q", data.Type.ValueString(), "api_key")
	}
	if data.CreatedBy.IsNull() {
		t.Error("CreatedBy should not be null")
	}
}

func TestMapAPIKeyToState_nullWorkspaceID(t *testing.T) {
	key := &apiKeyAPIResponse{
		ID:             "apikey_01ABC",
		Name:           "Org Key",
		Status:         "active",
		WorkspaceID:    nil,
		CreatedAt:      "2026-01-01T00:00:00Z",
		CreatedBy:      apiKeyCreatedBy{ID: "user_01XYZ", Type: "user"},
		PartialKeyHint: "abcd",
		Type:           "api_key",
	}

	var data APIKeyResourceModel
	diags := mapAPIKeyToState(context.Background(), key, &data)
	if diags.HasError() {
		t.Fatalf("mapAPIKeyToState error: %v", diags)
	}

	if !data.WorkspaceID.IsNull() {
		t.Errorf("WorkspaceID should be null, got %q", data.WorkspaceID.ValueString())
	}
}
