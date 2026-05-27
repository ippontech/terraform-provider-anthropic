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

	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestAPIKeyDataSource_read(t *testing.T) {
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

	var data APIKeyResourceModel
	data.ID = types.StringValue("apikey_01ABC")

	diags := mapAPIKeyToState(context.Background(), &key, &data)
	if diags.HasError() {
		t.Fatalf("mapAPIKeyToState errors: %v", diags)
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
}

func TestAPIKeyDataSource_notFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"type":"not_found_error","message":"api key not found"}}`)
	}))
	defer srv.Close()

	client := newTestAdminClient(t, srv)
	_, err := client.DoRequest(context.Background(), "GET", "/v1/organizations/api_keys/apikey_missing", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}
