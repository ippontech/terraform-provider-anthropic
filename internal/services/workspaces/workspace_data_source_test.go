// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
)

func TestWorkspaceDataSource_read(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/organizations/workspaces/wrkspc_01ABC" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, workspaceFixture())
	}))
	defer srv.Close()

	client := &admin.Client{
		ApiKey:     "test-key",
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
	}

	body, err := client.DoRequest(context.Background(), "GET", "/v1/organizations/workspaces/wrkspc_01ABC", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var ws workspaceAPIResponse
	if err := json.Unmarshal(body, &ws); err != nil {
		t.Fatalf("parse: %v", err)
	}

	var data WorkspaceResourceModel
	data.ID = types.StringValue(ws.ID)

	ctx := context.Background()
	diags := mapWorkspaceToState(ctx, &ws, &data)
	if diags.HasError() {
		t.Fatalf("mapWorkspaceToState errors: %v", diags)
	}

	if data.ID.ValueString() != "wrkspc_01ABC" {
		t.Errorf("ID = %q, want %q", data.ID.ValueString(), "wrkspc_01ABC")
	}
	if data.Name.ValueString() != "test-workspace" {
		t.Errorf("Name = %q, want %q", data.Name.ValueString(), "test-workspace")
	}
	if data.DisplayColor.ValueString() != "#FF5733" {
		t.Errorf("DisplayColor = %q, want %q", data.DisplayColor.ValueString(), "#FF5733")
	}
	if data.Type.ValueString() != "workspace" {
		t.Errorf("Type = %q, want %q", data.Type.ValueString(), "workspace")
	}
	if !data.ArchivedAt.IsNull() {
		t.Errorf("ArchivedAt should be null for active workspace, got %q", data.ArchivedAt.ValueString())
	}
	if data.DataResidency.IsNull() {
		t.Error("DataResidency should not be null")
	}
}

func TestWorkspaceDataSource_notFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"type":"not_found_error","message":"workspace not found"}}`)
	}))
	defer srv.Close()

	client := &admin.Client{
		ApiKey:     "test-key",
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
	}

	_, err := client.DoRequest(context.Background(), "GET", "/v1/organizations/workspaces/wrkspc_missing", nil)
	if !admin.IsNotFound(err) {
		t.Fatalf("expected IsNotFound, got: %v", err)
	}
}
