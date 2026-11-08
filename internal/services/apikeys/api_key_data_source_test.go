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

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/ippontech/terraform-provider-anthropic/internal/admintest"
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

func TestAPIKeyDataSource_ReadLegacyKeyKeepsEmptyStrings(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/organizations/api_keys/apikey_01LEGACY" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, legacyAPIKeyJSON)
	}))
	defer srv.Close()

	d := &APIKeyDataSource{adminClient: admintest.NewClient(t, srv)}
	var schemaResp datasource.SchemaResponse
	d.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema: %v", schemaResp.Diagnostics)
	}
	objType := schemaResp.Schema.Type().TerraformType(context.Background()).(tftypes.Object)
	vals := map[string]tftypes.Value{}
	for name, typ := range objType.AttributeTypes {
		vals[name] = tftypes.NewValue(typ, nil)
	}
	vals["id"] = tftypes.NewValue(tftypes.String, "apikey_01LEGACY")

	req := datasource.ReadRequest{Config: tfsdk.Config{Raw: tftypes.NewValue(objType, vals), Schema: schemaResp.Schema}}
	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	d.Read(context.Background(), req, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", resp.Diagnostics)
	}

	var data APIKeyResourceModel
	if diags := resp.State.Get(context.Background(), &data); diags.HasError() {
		t.Fatalf("state: %v", diags)
	}
	assertLegacyEmptyStrings(t, data.CreatedBy, data.PartialKeyHint)
	if !data.Principal.IsNull() {
		t.Errorf("principal = %v, want null", data.Principal)
	}
	if !data.ExpiresAt.IsNull() {
		t.Errorf("expires_at = %v, want null", data.ExpiresAt)
	}
}
