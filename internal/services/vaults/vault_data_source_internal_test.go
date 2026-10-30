// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package vaults

import (
	"context"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/ippontech/terraform-provider-anthropic/internal/oauthtest"
)

// readDataSource drives d.Read directly (bypassing Configure) with a config
// whose every attribute is null except overrides, and returns the response.
// Shared by the four vault data source tests of this package.
func readDataSource(t *testing.T, d datasource.DataSource, overrides map[string]tftypes.Value) *datasource.ReadResponse {
	t.Helper()

	var schemaResp datasource.SchemaResponse
	d.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema: %v", schemaResp.Diagnostics)
	}
	objType := schemaResp.Schema.Type().TerraformType(context.Background()).(tftypes.Object)

	vals := make(map[string]tftypes.Value, len(objType.AttributeTypes))
	for name, typ := range objType.AttributeTypes {
		vals[name] = tftypes.NewValue(typ, nil)
	}
	for name, v := range overrides {
		vals[name] = v
	}

	req := datasource.ReadRequest{Config: tfsdk.Config{
		Raw:    tftypes.NewValue(objType, vals),
		Schema: schemaResp.Schema,
	}}
	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	d.Read(context.Background(), req, resp)
	return resp
}

// schemaAttrTypes returns the attribute types d's schema declares, to check
// that a hand-written attr type map (used to build list elements) matches the
// schema it is meant to mirror.
func schemaAttrTypes(t *testing.T, d datasource.DataSource) map[string]attr.Type {
	t.Helper()
	var resp datasource.SchemaResponse
	d.Schema(context.Background(), datasource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("schema: %v", resp.Diagnostics)
	}
	return resp.Schema.Type().(types.ObjectType).AttrTypes
}

// requireManagedAgentsBeta fails the test unless r carries the beta header the
// vaults endpoints require.
func requireManagedAgentsBeta(t *testing.T, r *http.Request) {
	t.Helper()
	if !slices.Contains(r.Header.Values("anthropic-beta"), "managed-agents-2026-04-01") {
		t.Errorf("anthropic-beta = %v, want managed-agents-2026-04-01", r.Header.Values("anthropic-beta"))
	}
}

const vaultJSON = `{
	"id": "vlt_01ABC",
	"type": "vault",
	"display_name": "team-vault",
	"metadata": {"team": "platform"},
	"created_at": "2026-09-01T10:00:00Z",
	"updated_at": "2026-09-02T11:00:00Z",
	"archived_at": "2026-09-03T12:00:00Z"
}`

func TestMapVaultCommon(t *testing.T) {
	vault := &anthropic.BetaManagedAgentsVault{
		ID:          "vlt_01ABC",
		Type:        "vault",
		DisplayName: "team-vault",
		CreatedAt:   time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC),
		UpdatedAt:   time.Date(2026, 9, 2, 11, 0, 0, 0, time.UTC),
	}

	m, diags := mapVaultCommon(vault)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if m.ID.ValueString() != "vlt_01ABC" || m.DisplayName.ValueString() != "team-vault" || m.Type.ValueString() != "vault" {
		t.Errorf("unexpected identity fields: %+v", m)
	}
	if m.CreatedAt.ValueString() != "2026-09-01T10:00:00Z" || m.UpdatedAt.ValueString() != "2026-09-02T11:00:00Z" {
		t.Errorf("unexpected timestamps: created_at=%v updated_at=%v", m.CreatedAt, m.UpdatedAt)
	}
	if !m.ArchivedAt.IsNull() {
		t.Errorf("archived_at = %v, want null for a live vault", m.ArchivedAt)
	}
	if !m.Metadata.IsNull() || !m.Metadata.ElementType(context.Background()).Equal(types.StringType) {
		t.Errorf("metadata = %v, want a typed null map when the API returns none", m.Metadata)
	}
}

func TestVaultDataSourceRead_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/vaults/vlt_01ABC" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		}
		requireManagedAgentsBeta(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, vaultJSON)
	}))
	defer srv.Close()

	resp := readDataSource(t, &VaultDataSource{client: oauthtest.NewSDKClient(t, srv)}, map[string]tftypes.Value{
		"id": tftypes.NewValue(tftypes.String, "vlt_01ABC"),
	})
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}

	var got vaultModel
	resp.Diagnostics.Append(resp.State.Get(context.Background(), &got)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("state: %v", resp.Diagnostics)
	}
	if got.DisplayName.ValueString() != "team-vault" {
		t.Errorf("display_name = %v", got.DisplayName)
	}
	if got.ArchivedAt.ValueString() != "2026-09-03T12:00:00Z" {
		t.Errorf("archived_at = %v, want the archive timestamp (archived vaults are readable)", got.ArchivedAt)
	}
	if v, ok := got.Metadata.Elements()["team"].(types.String); !ok || v.ValueString() != "platform" {
		t.Errorf("metadata = %v, want team=platform", got.Metadata)
	}
}

func TestVaultDataSourceRead_notFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"not_found_error","message":"Vault not found"}}`)
	}))
	defer srv.Close()

	resp := readDataSource(t, &VaultDataSource{client: oauthtest.NewSDKClient(t, srv)}, map[string]tftypes.Value{
		"id": tftypes.NewValue(tftypes.String, "vlt_missing"),
	})
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error diagnostic for a 404")
	}
	if got := resp.Diagnostics.Errors()[0].Summary(); got != "Vault Not Found" {
		t.Errorf("summary = %q, want Vault Not Found", got)
	}
}

// TestVaultAttrTypesMatchSchema pins the list element type of anthropic_vaults
// to the anthropic_vault schema both are built from, so an attribute added to
// one cannot silently drift from the other.
func TestVaultAttrTypesMatchSchema(t *testing.T) {
	got := schemaAttrTypes(t, &VaultDataSource{})
	if !maps.EqualFunc(got, vaultAttrTypes, func(a, b attr.Type) bool { return a.Equal(b) }) {
		t.Errorf("vaultAttrTypes = %v, schema declares %v", vaultAttrTypes, got)
	}
}
