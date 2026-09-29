// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package userprofiles

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/ippontech/terraform-provider-anthropic/internal/oauthtest"
	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
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

// --- buildUserProfileCreateParams / buildUserProfileUpdateParams ---

func TestBuildUserProfileCreateParams_Basic(t *testing.T) {
	ctx := context.Background()
	meta, d := types.MapValueFrom(ctx, types.StringType, map[string]string{"tier": "gold"})
	if d.HasError() {
		t.Fatalf("unexpected diagnostics: %v", d)
	}

	data := UserProfileResourceModel{
		AccessType: types.StringValue("application"),
		ExternalID: types.StringValue("cust-1"),
		Name:       types.StringValue("Acme"),
		Metadata:   meta,
	}

	params, diags := buildUserProfileCreateParams(ctx, data)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if params.AccessType != anthropic.BetaUserProfileNewParamsAccessTypeApplication {
		t.Errorf("expected access_type application, got %v", params.AccessType)
	}
	if !params.ExternalID.Valid() || params.ExternalID.Value != "cust-1" {
		t.Errorf("expected external_id cust-1, got %v", params.ExternalID)
	}
	if !params.Name.Valid() || params.Name.Value != "Acme" {
		t.Errorf("expected name Acme, got %v", params.Name)
	}
	if params.Metadata["tier"] != "gold" {
		t.Errorf("expected metadata tier=gold, got %v", params.Metadata)
	}
}

func TestBuildUserProfileCreateParams_OptionalFieldsOmitted(t *testing.T) {
	ctx := context.Background()
	data := UserProfileResourceModel{
		AccessType: types.StringValue("passthrough"),
		ExternalID: types.StringNull(),
		Name:       types.StringNull(),
		Metadata:   types.MapNull(types.StringType),
	}

	params, diags := buildUserProfileCreateParams(ctx, data)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if params.ExternalID.Valid() {
		t.Errorf("expected external_id to be omitted, got %v", params.ExternalID)
	}
	if params.Name.Valid() {
		t.Errorf("expected name to be omitted, got %v", params.Name)
	}
	if params.Metadata != nil {
		t.Errorf("expected metadata to be omitted, got %v", params.Metadata)
	}

	body, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("unexpected error marshalling params: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("unexpected error unmarshalling body: %v", err)
	}
	if _, present := decoded["external_id"]; present {
		t.Errorf("expected external_id to be omitted from the request body, got %v", decoded)
	}
	if _, present := decoded["name"]; present {
		t.Errorf("expected name to be omitted from the request body, got %v", decoded)
	}
}

func TestBuildUserProfileUpdateParams_UnchangedFieldsOmitted(t *testing.T) {
	ctx := context.Background()
	state := UserProfileResourceModel{
		AccessType: types.StringValue("application"),
		ExternalID: types.StringValue("cust-1"),
		Name:       types.StringValue("Acme"),
		Metadata:   types.MapNull(types.StringType),
	}
	// Nothing changed from state.
	data := state

	params, diags := buildUserProfileUpdateParams(ctx, data, state)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	body, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("unexpected error marshalling params: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("unexpected error unmarshalling body: %v", err)
	}
	if _, present := decoded["external_id"]; present {
		t.Errorf("expected external_id to be omitted from the update body since it did not change, got %v", decoded)
	}
	if _, present := decoded["name"]; present {
		t.Errorf("expected name to be omitted from the update body since it did not change, got %v", decoded)
	}
}

func TestBuildUserProfileUpdateParams_ClearedFieldsSendJSONNull(t *testing.T) {
	ctx := context.Background()
	state := UserProfileResourceModel{
		AccessType: types.StringValue("application"),
		ExternalID: types.StringValue("cust-1"),
		Name:       types.StringValue("Acme"),
		Metadata:   types.MapNull(types.StringType),
	}
	data := state
	data.ExternalID = types.StringNull()
	data.Name = types.StringNull()

	params, diags := buildUserProfileUpdateParams(ctx, data, state)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	body, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("unexpected error marshalling params: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("unexpected error unmarshalling body: %v", err)
	}
	// A key present with a JSON null value, not an empty string and not
	// omitted, is what the API needs to clear a nullable field.
	extVal, present := decoded["external_id"]
	if !present {
		t.Fatalf("expected external_id in request body, got %v", decoded)
	}
	if extVal != nil {
		t.Errorf(`expected external_id to marshal to JSON null, got %#v`, extVal)
	}
	nameVal, present := decoded["name"]
	if !present {
		t.Fatalf("expected name in request body, got %v", decoded)
	}
	if nameVal != nil {
		t.Errorf(`expected name to marshal to JSON null, got %#v`, nameVal)
	}
}

func TestBuildUserProfileUpdateParams_ChangedFieldsSendNewValue(t *testing.T) {
	ctx := context.Background()
	state := UserProfileResourceModel{
		AccessType: types.StringValue("application"),
		ExternalID: types.StringValue("cust-1"),
		Name:       types.StringValue("Acme"),
		Metadata:   types.MapNull(types.StringType),
	}
	data := state
	data.AccessType = types.StringValue("passthrough")
	data.ExternalID = types.StringValue("cust-2")

	params, diags := buildUserProfileUpdateParams(ctx, data, state)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if params.AccessType != anthropic.BetaUserProfileUpdateParamsAccessTypePassthrough {
		t.Errorf("expected access_type passthrough, got %v", params.AccessType)
	}
	if !params.ExternalID.Valid() || params.ExternalID.Value != "cust-2" {
		t.Errorf("expected external_id cust-2, got %v", params.ExternalID)
	}
}

// --- Resource-level Create/Read/Update wiring, driven through the framework
// request/response types (not just the SDK client), since these are the only
// exercise this code gets without a live acceptance run. ---

func TestUserProfileResource_Create(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotMethod = req.Method
		gotPath = req.URL.Path
		_ = json.NewDecoder(req.Body).Decode(&gotBody)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(anthropic.BetaUserProfile{
			ID:         "uprof_created",
			AccessType: anthropic.BetaUserProfileAccessTypeApplication,
			Type:       anthropic.BetaUserProfileTypeUserProfile,
			ExternalID: "cust-1",
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		})
	}))
	defer srv.Close()

	r := &UserProfileResource{client: oauthtest.NewSDKClient(t, srv)}

	objType := schematest.ResourceObjectType(t, r)
	schema := schematest.ResourceSchema(t, r)
	vals := schematest.NullValues(t, r)
	vals["access_type"] = tftypes.NewValue(objType.AttributeTypes["access_type"], "application")
	vals["external_id"] = tftypes.NewValue(objType.AttributeTypes["external_id"], "cust-1")
	rawVal := tftypes.NewValue(objType, vals)

	req := resource.CreateRequest{Plan: tfsdk.Plan{Raw: rawVal, Schema: schema}}
	resp := &resource.CreateResponse{State: tfsdk.State{Raw: rawVal, Schema: schema}}

	r.Create(context.Background(), req, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create returned errors: %v", resp.Diagnostics)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("expected POST, got %s", gotMethod)
	}
	if gotPath != "/v1/user_profiles" {
		t.Errorf("expected path /v1/user_profiles, got %s", gotPath)
	}
	if gotBody["access_type"] != "application" {
		t.Errorf("expected access_type=application in request body, got %v", gotBody)
	}
	if gotBody["external_id"] != "cust-1" {
		t.Errorf("expected external_id=cust-1 in request body, got %v", gotBody)
	}

	var data UserProfileResourceModel
	if diags := resp.State.Get(context.Background(), &data); diags.HasError() {
		t.Fatalf("failed to read state: %v", diags)
	}
	if data.ID.ValueString() != "uprof_created" {
		t.Errorf("expected id uprof_created, got %s", data.ID.ValueString())
	}
}

func TestUserProfileResource_Read_NotFoundRemovesFromStateWithWarning(t *testing.T) {
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

	r := &UserProfileResource{client: oauthtest.NewSDKClient(t, srv)}

	objType := schematest.ResourceObjectType(t, r)
	schema := schematest.ResourceSchema(t, r)
	vals := schematest.NullValues(t, r)
	vals["id"] = tftypes.NewValue(objType.AttributeTypes["id"], "uprof_missing")
	vals["access_type"] = tftypes.NewValue(objType.AttributeTypes["access_type"], "application")
	rawVal := tftypes.NewValue(objType, vals)
	state := tfsdk.State{Raw: rawVal, Schema: schema}

	resp := &resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read returned errors: %v", resp.Diagnostics)
	}
	if !resp.State.Raw.IsNull() {
		t.Error("expected state to be removed (null) after a 404, but it was not")
	}
	foundWarning := false
	for _, d := range resp.Diagnostics {
		if d.Summary() == "User Profile Not Found" {
			foundWarning = true
		}
	}
	if !foundWarning {
		t.Errorf("expected a warning diagnostic explaining the ambiguous 404, got %v", resp.Diagnostics)
	}
}

func TestUserProfileResource_Update(t *testing.T) {
	var gotMethod, gotPath string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotMethod = req.Method
		gotPath = req.URL.Path
		_ = json.NewDecoder(req.Body).Decode(&gotBody)

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(anthropic.BetaUserProfile{
			ID:         "uprof_01ABC",
			AccessType: anthropic.BetaUserProfileAccessTypeApplication,
			Type:       anthropic.BetaUserProfileTypeUserProfile,
			CreatedAt:  time.Now(),
			UpdatedAt:  time.Now(),
		})
	}))
	defer srv.Close()

	r := &UserProfileResource{client: oauthtest.NewSDKClient(t, srv)}

	objType := schematest.ResourceObjectType(t, r)
	schema := schematest.ResourceSchema(t, r)

	stateVals := schematest.NullValues(t, r)
	stateVals["id"] = tftypes.NewValue(objType.AttributeTypes["id"], "uprof_01ABC")
	stateVals["access_type"] = tftypes.NewValue(objType.AttributeTypes["access_type"], "application")
	stateVals["name"] = tftypes.NewValue(objType.AttributeTypes["name"], "Old Name")
	stateRaw := tftypes.NewValue(objType, stateVals)

	planVals := schematest.NullValues(t, r)
	planVals["id"] = tftypes.NewValue(objType.AttributeTypes["id"], "uprof_01ABC")
	planVals["access_type"] = tftypes.NewValue(objType.AttributeTypes["access_type"], "application")
	// name removed from config: the plan carries it as null.
	planRaw := tftypes.NewValue(objType, planVals)

	req := resource.UpdateRequest{
		Plan:  tfsdk.Plan{Raw: planRaw, Schema: schema},
		State: tfsdk.State{Raw: stateRaw, Schema: schema},
	}
	resp := &resource.UpdateResponse{State: tfsdk.State{Schema: schema}}

	r.Update(context.Background(), req, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Update returned errors: %v", resp.Diagnostics)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("expected POST, got %s", gotMethod)
	}
	if gotPath != "/v1/user_profiles/uprof_01ABC" {
		t.Errorf("expected path /v1/user_profiles/uprof_01ABC, got %s", gotPath)
	}
	nameVal, present := gotBody["name"]
	if !present {
		t.Fatalf("expected name in the update request body, got %v", gotBody)
	}
	if nameVal != nil {
		t.Errorf("expected name to be sent as JSON null to clear it, got %#v", nameVal)
	}
}
