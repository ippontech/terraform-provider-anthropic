// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package userprofiles

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/ippontech/terraform-provider-anthropic/internal/oauthtest"
)

func TestMapUserProfileDSToModel_basic(t *testing.T) {
	createdAt := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC)
	profile := &anthropic.BetaUserProfile{
		ID:         "uprof_01ABC",
		CreatedAt:  createdAt,
		UpdatedAt:  updatedAt,
		Type:       anthropic.BetaUserProfileTypeUserProfile,
		AccessType: anthropic.BetaUserProfileAccessTypeApplication,
		ExternalID: "ext-123",
		Name:       "Jane Doe",
		Metadata:   map[string]string{"team": "platform"},
		TrustGrants: map[string]anthropic.BetaUserProfileTrustGrant{
			"grant_a": {Status: anthropic.BetaUserProfileTrustGrantStatusActive},
		},
	}

	m, diags := mapUserProfileDSToModel(profile)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if got := m.ID.ValueString(); got != "uprof_01ABC" {
		t.Errorf("id = %q, want uprof_01ABC", got)
	}
	if got := m.AccessType.ValueString(); got != "application" {
		t.Errorf("access_type = %q, want application", got)
	}
	if got := m.ExternalID.ValueString(); got != "ext-123" {
		t.Errorf("external_id = %q, want ext-123", got)
	}
	if got := m.Name.ValueString(); got != "Jane Doe" {
		t.Errorf("name = %q, want Jane Doe", got)
	}
	if got := m.CreatedAt.ValueString(); got != "2026-08-01T00:00:00Z" {
		t.Errorf("created_at = %q, want 2026-08-01T00:00:00Z", got)
	}
	if got := m.UpdatedAt.ValueString(); got != "2026-08-02T00:00:00Z" {
		t.Errorf("updated_at = %q, want 2026-08-02T00:00:00Z", got)
	}
	if m.Metadata.IsNull() {
		t.Fatal("metadata should not be null")
	}
	if got, ok := m.Metadata.Elements()["team"].(types.String); !ok || got.ValueString() != "platform" {
		t.Errorf("metadata[team] = %v, want platform", m.Metadata.Elements()["team"])
	}
	if m.TrustGrants.IsNull() {
		t.Fatal("trust_grants should not be null")
	}
	grant, ok := m.TrustGrants.Elements()["grant_a"].(types.Object)
	if !ok {
		t.Fatalf("trust_grants[grant_a] has unexpected type %T", m.TrustGrants.Elements()["grant_a"])
	}
	if got := grant.Attributes()["status"].(types.String).ValueString(); got != "active" {
		t.Errorf("trust_grants[grant_a].status = %q, want active", got)
	}
}

func TestMapUserProfileDSToModel_nullableFieldsAndEmptyMaps(t *testing.T) {
	profile := &anthropic.BetaUserProfile{
		ID:         "uprof_02DEF",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
		Type:       anthropic.BetaUserProfileTypeUserProfile,
		AccessType: anthropic.BetaUserProfileAccessTypePassthrough,
	}

	m, diags := mapUserProfileDSToModel(profile)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if !m.ExternalID.IsNull() {
		t.Error("external_id should be null when the API returns an empty string")
	}
	if !m.Name.IsNull() {
		t.Error("name should be null when the API returns an empty string")
	}
	if !m.Metadata.IsNull() {
		t.Error("metadata should be null when the API returns an empty map")
	}
	if !m.TrustGrants.IsNull() {
		t.Error("trust_grants should be null when the API returns an empty map")
	}
}

// TestUserProfileDataSourceRead_notFound drives UserProfileDataSource.Read
// itself (not just the SDK call it wraps) against a 404 stub, so a future
// change that swallowed the error instead of surfacing a diagnostic would
// fail this test.
func TestUserProfileDataSourceRead_notFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"not_found_error","message":"User profile not found"}}`)
	}))
	defer srv.Close()

	d := &UserProfileDataSource{client: oauthtest.NewSDKClient(t, srv)}

	var schemaResp datasource.SchemaResponse
	d.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema: %v", schemaResp.Diagnostics)
	}

	objType, ok := schemaResp.Schema.Type().(interface {
		TerraformType(context.Context) tftypes.Type
	})
	if !ok {
		t.Fatal("schema type does not implement TerraformType")
	}
	tfObjType, ok := objType.TerraformType(context.Background()).(tftypes.Object)
	if !ok {
		t.Fatal("schema type is not a tftypes.Object")
	}

	vals := make(map[string]tftypes.Value, len(tfObjType.AttributeTypes))
	for name, typ := range tfObjType.AttributeTypes {
		vals[name] = tftypes.NewValue(typ, nil)
	}
	vals["id"] = tftypes.NewValue(tftypes.String, "uprof_missing")

	req := datasource.ReadRequest{
		Config: tfsdk.Config{
			Raw:    tftypes.NewValue(tfObjType, vals),
			Schema: schemaResp.Schema,
		},
	}
	var resp datasource.ReadResponse
	resp.State = tfsdk.State{Schema: schemaResp.Schema}

	d.Read(context.Background(), req, &resp)

	if !resp.Diagnostics.HasError() {
		t.Fatal("expected Read to report an error diagnostic for a 404 response")
	}
}
