// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package userprofiles

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ippontech/terraform-provider-anthropic/internal/oauthtest"
)

func TestMapUserProfileToState_BasicFields(t *testing.T) {
	createdAt := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2024, 1, 16, 11, 0, 0, 0, time.UTC)

	profile := &anthropic.BetaUserProfile{
		ID:         "uprof_01ABC",
		AccessType: anthropic.BetaUserProfileAccessTypeApplication,
		Type:       anthropic.BetaUserProfileTypeUserProfile,
		CreatedAt:  createdAt,
		UpdatedAt:  updatedAt,
	}

	var data UserProfileResourceModel
	diags := mapUserProfileToState(profile, &data)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if data.ID.ValueString() != "uprof_01ABC" {
		t.Errorf("expected ID uprof_01ABC, got %s", data.ID.ValueString())
	}
	if data.AccessType.ValueString() != "application" {
		t.Errorf("expected AccessType application, got %s", data.AccessType.ValueString())
	}
	if data.Type.ValueString() != "user_profile" {
		t.Errorf("expected Type user_profile, got %s", data.Type.ValueString())
	}
	if data.CreatedAt.ValueString() != "2024-01-15T10:00:00Z" {
		t.Errorf("expected CreatedAt 2024-01-15T10:00:00Z, got %s", data.CreatedAt.ValueString())
	}
	if data.UpdatedAt.ValueString() != "2024-01-16T11:00:00Z" {
		t.Errorf("expected UpdatedAt 2024-01-16T11:00:00Z, got %s", data.UpdatedAt.ValueString())
	}
	if !data.ExternalID.IsNull() {
		t.Errorf("expected ExternalID null, got %s", data.ExternalID.ValueString())
	}
	if !data.Name.IsNull() {
		t.Errorf("expected Name null, got %s", data.Name.ValueString())
	}
	if !data.Metadata.IsNull() {
		t.Errorf("expected Metadata null, got %v", data.Metadata)
	}
	if data.TrustGrants.IsNull() {
		t.Error("expected TrustGrants to be a non-null empty map, not null")
	}
	if len(data.TrustGrants.Elements()) != 0 {
		t.Errorf("expected TrustGrants to be empty, got %v", data.TrustGrants.Elements())
	}
}

func TestMapUserProfileToState_ExternalIDAndNamePopulated(t *testing.T) {
	profile := &anthropic.BetaUserProfile{
		ID:         "uprof_02DEF",
		AccessType: anthropic.BetaUserProfileAccessTypePassthrough,
		Type:       anthropic.BetaUserProfileTypeUserProfile,
		ExternalID: "cust-123",
		Name:       "Acme Corp",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	var data UserProfileResourceModel
	diags := mapUserProfileToState(profile, &data)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if data.ExternalID.ValueString() != "cust-123" {
		t.Errorf("expected ExternalID cust-123, got %s", data.ExternalID.ValueString())
	}
	if data.Name.ValueString() != "Acme Corp" {
		t.Errorf("expected Name 'Acme Corp', got %s", data.Name.ValueString())
	}
}

func TestMapUserProfileToState_MetadataPopulated(t *testing.T) {
	profile := &anthropic.BetaUserProfile{
		ID:         "uprof_03GHI",
		AccessType: anthropic.BetaUserProfileAccessTypeApplication,
		Type:       anthropic.BetaUserProfileTypeUserProfile,
		Metadata:   map[string]string{"tier": "gold"},
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
	}

	var data UserProfileResourceModel
	diags := mapUserProfileToState(profile, &data)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if data.Metadata.IsNull() {
		t.Fatal("expected Metadata to be non-null")
	}
	var meta map[string]string
	diags = data.Metadata.ElementsAs(context.Background(), &meta, false)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if meta["tier"] != "gold" {
		t.Errorf("expected metadata tier=gold, got %v", meta)
	}
}

func TestMapUserProfileToState_TrustGrantsPopulated(t *testing.T) {
	profile := &anthropic.BetaUserProfile{
		ID:         "uprof_04JKL",
		AccessType: anthropic.BetaUserProfileAccessTypeApplication,
		Type:       anthropic.BetaUserProfileTypeUserProfile,
		TrustGrants: map[string]anthropic.BetaUserProfileTrustGrant{
			"kyc": {Status: anthropic.BetaUserProfileTrustGrantStatusActive},
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	var data UserProfileResourceModel
	diags := mapUserProfileToState(profile, &data)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	elements := data.TrustGrants.Elements()
	grant, ok := elements["kyc"]
	if !ok {
		t.Fatalf("expected a 'kyc' trust grant, got %v", elements)
	}
	obj, ok := grant.(types.Object)
	if !ok {
		t.Fatalf("expected trust grant to be an Object, got %T", grant)
	}
	status, ok := obj.Attributes()["status"].(types.String)
	if !ok || status.ValueString() != "active" {
		t.Errorf("expected status=active, got %v", obj.Attributes())
	}
}

func TestPreserveEmptyMetadata(t *testing.T) {
	known, d := types.MapValueFrom(context.Background(), types.StringType, map[string]string{})
	if d.HasError() {
		t.Fatalf("unexpected diagnostics: %v", d)
	}
	apiNull := types.MapNull(types.StringType)

	result := preserveEmptyMetadata(known, apiNull)
	if result.IsNull() {
		t.Error("expected preserveEmptyMetadata to keep the known empty map, not null")
	}

	nonEmptyKnown, d := types.MapValueFrom(context.Background(), types.StringType, map[string]string{"k": "v"})
	if d.HasError() {
		t.Fatalf("unexpected diagnostics: %v", d)
	}
	populated, d := types.MapValueFrom(context.Background(), types.StringType, map[string]string{"k": "v2"})
	if d.HasError() {
		t.Fatalf("unexpected diagnostics: %v", d)
	}
	result = preserveEmptyMetadata(nonEmptyKnown, populated)
	if !result.Equal(populated) {
		t.Errorf("expected preserveEmptyMetadata to pass through a populated API result, got %v", result)
	}
}

func TestBuildUserProfileMetadataUpdate_UpsertsAndRemoves(t *testing.T) {
	ctx := context.Background()

	plan, d := types.MapValueFrom(ctx, types.StringType, map[string]string{"a": "1", "b": "2"})
	if d.HasError() {
		t.Fatalf("unexpected diagnostics: %v", d)
	}
	state, d := types.MapValueFrom(ctx, types.StringType, map[string]string{"a": "0", "c": "3"})
	if d.HasError() {
		t.Fatalf("unexpected diagnostics: %v", d)
	}

	update, diags := buildUserProfileMetadataUpdate(ctx, plan, state)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	want := map[string]string{"a": "1", "b": "2", "c": ""}
	if len(update) != len(want) {
		t.Fatalf("expected %v, got %v", want, update)
	}
	for k, v := range want {
		if update[k] != v {
			t.Errorf("key %s: expected %q, got %q", k, v, update[k])
		}
	}
}

func TestBuildUserProfileMetadataUpdate_NoChange(t *testing.T) {
	ctx := context.Background()
	planNull := types.MapNull(types.StringType)
	stateNull := types.MapNull(types.StringType)

	update, diags := buildUserProfileMetadataUpdate(ctx, planNull, stateNull)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if len(update) != 0 {
		t.Errorf("expected no changes, got %v", update)
	}
}

func TestUserProfileResource_Create_WiresRequest(t *testing.T) {
	var capturedPath string
	var capturedMethod string
	var capturedBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		capturedPath = req.URL.Path
		capturedMethod = req.Method
		_ = json.NewDecoder(req.Body).Decode(&capturedBody)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(anthropic.BetaUserProfile{
			ID:         "uprof_created",
			AccessType: anthropic.BetaUserProfileAccessTypeApplication,
			Type:       anthropic.BetaUserProfileTypeUserProfile,
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		})
	}))
	defer srv.Close()

	client := oauthtest.NewSDKClient(t, srv)

	profile, err := client.Beta.UserProfiles.New(context.Background(), anthropic.BetaUserProfileNewParams{
		AccessType: anthropic.BetaUserProfileNewParamsAccessTypeApplication,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if profile.ID != "uprof_created" {
		t.Errorf("expected uprof_created, got %s", profile.ID)
	}
	if capturedMethod != http.MethodPost {
		t.Errorf("expected POST, got %s", capturedMethod)
	}
	if capturedPath != "/v1/user_profiles" {
		t.Errorf("expected path /v1/user_profiles, got %s", capturedPath)
	}
	if capturedBody["access_type"] != "application" {
		t.Errorf("expected access_type=application in request body, got %v", capturedBody)
	}
}

func TestUserProfileResource_Read_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    "not_found_error",
				"message": "user profile not found",
			},
		})
	}))
	defer srv.Close()

	client := oauthtest.NewSDKClient(t, srv)

	_, err := client.Beta.UserProfiles.Get(context.Background(), "uprof_missing", anthropic.BetaUserProfileGetParams{})
	if err == nil {
		t.Fatal("expected an error")
	}
	var apierr *anthropic.Error
	if !errors.As(err, &apierr) || apierr.StatusCode != http.StatusNotFound {
		t.Fatalf("expected a 404 *anthropic.Error, got %v", err)
	}
}
