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
	"github.com/hashicorp/terraform-plugin-framework/types"
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

func TestUserProfileDataSourceRead_notFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"not_found_error","message":"User profile not found"}}`)
	}))
	defer srv.Close()

	client := oauthtest.NewSDKClient(t, srv)
	_, err := client.Beta.UserProfiles.Get(context.Background(), "uprof_missing", anthropic.BetaUserProfileGetParams{})
	if err == nil {
		t.Fatal("expected an error for a 404 response")
	}
}
