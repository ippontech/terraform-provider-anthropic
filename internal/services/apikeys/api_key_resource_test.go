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

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	"github.com/ippontech/terraform-provider-anthropic/internal/admintest"
	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
)

func newTestAdminClient(t *testing.T, srv *httptest.Server) *admin.Client {
	t.Helper()
	return admintest.NewClient(t, srv)
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
		"scope": {"type": "workspace", "workspace_id": "wrkspc_01WS"},
		"principal": {"type": "user_actor", "user_id": "user_01XYZ"},
		"expires_at": null,
		"created_at": "2026-01-01T00:00:00Z",
		"created_by": {"id": "user_01XYZ", "type": "user"},
		"partial_key_hint": "abcd",
		"type": "api_key"
	}`
}

// objectString returns the string attribute name of obj, failing the test if
// obj is null or the attribute is not a types.String.
func objectString(t *testing.T, obj types.Object, name string) types.String {
	t.Helper()
	if obj.IsNull() || obj.IsUnknown() {
		t.Fatalf("object is null or unknown, want attribute %q", name)
	}
	v, ok := obj.Attributes()[name].(types.String)
	if !ok {
		t.Fatalf("attribute %q is %T, want types.String", name, obj.Attributes()[name])
	}
	return v
}

func TestAPIKeyRead_parsesResponse(t *testing.T) {
	wsID := "wrkspc_01WS"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/organizations/api_keys/apikey_01ABC" {
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
		if r.Method != http.MethodPost || r.URL.Path != "/v1/organizations/api_keys/apikey_01ABC" {
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
		CreatedBy:      &apiKeyCreatedBy{ID: "user_01XYZ", Type: "user"},
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
		CreatedBy:      &apiKeyCreatedBy{ID: "user_01XYZ", Type: "user"},
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

// The Default Workspace case: workspace_id is null, scope carries the real ID.
func TestMapAPIKeyToState_workspaceScopeUserPrincipalExpiry(t *testing.T) {
	body := `{
		"id": "apikey_01ABC",
		"name": "Default WS key",
		"status": "expired",
		"workspace_id": null,
		"scope": {"type": "workspace", "workspace_id": "wrkspc_01DEFAULT"},
		"principal": {"type": "user_actor", "user_id": "user_01XYZ"},
		"expires_at": "2026-10-01T00:00:00Z",
		"created_at": "2026-01-01T00:00:00Z",
		"created_by": {"id": "user_01XYZ", "type": "user"},
		"partial_key_hint": "sk-ant-api03-R2D...igAA",
		"type": "api_key"
	}`
	var key apiKeyAPIResponse
	if err := json.Unmarshal([]byte(body), &key); err != nil {
		t.Fatalf("parse: %v", err)
	}

	var data APIKeyResourceModel
	if diags := mapAPIKeyToState(context.Background(), &key, &data); diags.HasError() {
		t.Fatalf("mapAPIKeyToState error: %v", diags)
	}

	if !data.WorkspaceID.IsNull() {
		t.Errorf("WorkspaceID = %q, want null", data.WorkspaceID.ValueString())
	}
	if got := objectString(t, data.Scope, "type").ValueString(); got != "workspace" {
		t.Errorf("scope.type = %q, want workspace", got)
	}
	if got := objectString(t, data.Scope, "workspace_id").ValueString(); got != "wrkspc_01DEFAULT" {
		t.Errorf("scope.workspace_id = %q, want wrkspc_01DEFAULT", got)
	}
	if got := objectString(t, data.Principal, "type").ValueString(); got != "user_actor" {
		t.Errorf("principal.type = %q, want user_actor", got)
	}
	if got := objectString(t, data.Principal, "user_id").ValueString(); got != "user_01XYZ" {
		t.Errorf("principal.user_id = %q, want user_01XYZ", got)
	}
	if got := objectString(t, data.Principal, "service_account_id"); !got.IsNull() {
		t.Errorf("principal.service_account_id = %v, want null", got)
	}
	if got := data.ExpiresAt.ValueString(); got != "2026-10-01T00:00:00Z" {
		t.Errorf("ExpiresAt = %q, want 2026-10-01T00:00:00Z", got)
	}
	if got := data.Status.ValueString(); got != "expired" {
		t.Errorf("Status = %q, want expired", got)
	}
}

func TestMapAPIKeyToState_organizationScopeServiceAccountPrincipal(t *testing.T) {
	body := `{
		"id": "apikey_01ORG",
		"name": "Org key",
		"status": "active",
		"workspace_id": null,
		"scope": {"type": "organization"},
		"principal": {"type": "service_account_actor", "service_account_id": "svac_01ABC"},
		"expires_at": null,
		"created_at": "2026-01-01T00:00:00Z",
		"created_by": {"id": "svac_01ABC", "type": "service_account"},
		"partial_key_hint": "abcd",
		"type": "api_key"
	}`
	var key apiKeyAPIResponse
	if err := json.Unmarshal([]byte(body), &key); err != nil {
		t.Fatalf("parse: %v", err)
	}

	var data APIKeyResourceModel
	if diags := mapAPIKeyToState(context.Background(), &key, &data); diags.HasError() {
		t.Fatalf("mapAPIKeyToState error: %v", diags)
	}

	if got := objectString(t, data.Scope, "type").ValueString(); got != "organization" {
		t.Errorf("scope.type = %q, want organization", got)
	}
	if got := objectString(t, data.Scope, "workspace_id"); !got.IsNull() {
		t.Errorf("scope.workspace_id = %v, want null", got)
	}
	if got := objectString(t, data.Principal, "type").ValueString(); got != "service_account_actor" {
		t.Errorf("principal.type = %q, want service_account_actor", got)
	}
	if got := objectString(t, data.Principal, "service_account_id").ValueString(); got != "svac_01ABC" {
		t.Errorf("principal.service_account_id = %q, want svac_01ABC", got)
	}
	if got := objectString(t, data.Principal, "user_id"); !got.IsNull() {
		t.Errorf("principal.user_id = %v, want null", got)
	}
	if !data.ExpiresAt.IsNull() {
		t.Errorf("ExpiresAt = %q, want null", data.ExpiresAt.ValueString())
	}
	if got := objectString(t, data.CreatedBy, "type").ValueString(); got != "service_account" {
		t.Errorf("created_by.type = %q, want service_account", got)
	}
}

// A legacy key: no principal, no recorded creator, no hint. Every one of them
// must map to null, never to an object or a string that is empty.
func TestMapAPIKeyToState_nullableFieldsMapToNull(t *testing.T) {
	body := `{
		"id": "apikey_01LEGACY",
		"name": "Legacy key",
		"status": "active",
		"workspace_id": "wrkspc_01WS",
		"scope": {"type": "workspace", "workspace_id": "wrkspc_01WS"},
		"principal": null,
		"expires_at": null,
		"created_at": "2024-01-01T00:00:00Z",
		"created_by": null,
		"partial_key_hint": null,
		"type": "api_key"
	}`
	var key apiKeyAPIResponse
	if err := json.Unmarshal([]byte(body), &key); err != nil {
		t.Fatalf("parse: %v", err)
	}

	var data APIKeyResourceModel
	if diags := mapAPIKeyToState(context.Background(), &key, &data); diags.HasError() {
		t.Fatalf("mapAPIKeyToState error: %v", diags)
	}

	if !data.Principal.IsNull() {
		t.Errorf("Principal = %v, want null", data.Principal)
	}
	// created_by stays an object, so that a configuration reading
	// created_by.id gets null instead of failing on a null object.
	if id := objectString(t, data.CreatedBy, "id"); !id.IsNull() {
		t.Errorf("CreatedBy.id = %v, want null", id)
	}
	if typ := objectString(t, data.CreatedBy, "type"); !typ.IsNull() {
		t.Errorf("CreatedBy.type = %v, want null", typ)
	}
	if !data.PartialKeyHint.IsNull() {
		t.Errorf("PartialKeyHint = %q, want null", data.PartialKeyHint.ValueString())
	}
	if !data.ExpiresAt.IsNull() {
		t.Errorf("ExpiresAt = %q, want null", data.ExpiresAt.ValueString())
	}
	if got := data.WorkspaceID.ValueString(); got != "wrkspc_01WS" {
		t.Errorf("WorkspaceID = %q, want wrkspc_01WS", got)
	}
}

// State written before null mapping holds created_by = {id = "", type = ""}
// and partial_key_hint = "" for a key with no recorded creator or hint.
// Under -refresh=false UseStateForUnknown plans them as they are, so Update
// must return them unchanged although the API now sends null: anything else
// is an inconsistent result after apply.
func TestAPIKeyResource_UpdateKeepsLegacyEmptyValues(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/organizations/api_keys/apikey_01LEGACY" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{
			"id": "apikey_01LEGACY", "name": "Renamed", "status": "active", "workspace_id": "wrkspc_01WS",
			"scope": {"type": "workspace", "workspace_id": "wrkspc_01WS"}, "principal": null,
			"expires_at": null, "created_at": "2024-01-01T00:00:00Z",
			"created_by": null, "partial_key_hint": null, "type": "api_key"
		}`)
	}))
	defer srv.Close()

	r := &APIKeyResource{adminClient: admintest.NewClient(t, srv)}
	s := schematest.ResourceSchema(t, r)
	objType := schematest.ResourceObjectType(t, r)
	createdByType := objType.AttributeTypes["created_by"]
	vals := schematest.NullValues(t, r)
	vals["id"] = tftypes.NewValue(tftypes.String, "apikey_01LEGACY")
	vals["name"] = tftypes.NewValue(tftypes.String, "Legacy key")
	vals["status"] = tftypes.NewValue(tftypes.String, "active")
	vals["created_by"] = tftypes.NewValue(createdByType, map[string]tftypes.Value{
		"id":   tftypes.NewValue(tftypes.String, ""),
		"type": tftypes.NewValue(tftypes.String, ""),
	})
	vals["partial_key_hint"] = tftypes.NewValue(tftypes.String, "")
	state := tfsdk.State{Raw: tftypes.NewValue(objType, vals), Schema: s}
	vals["name"] = tftypes.NewValue(tftypes.String, "Renamed")
	plan := tfsdk.Plan{Raw: tftypes.NewValue(objType, vals), Schema: s}

	resp := resource.UpdateResponse{State: tfsdk.State{Raw: plan.Raw, Schema: s}}
	r.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Update returned errors: %v", resp.Diagnostics)
	}

	var data APIKeyResourceModel
	if diags := resp.State.Get(context.Background(), &data); diags.HasError() {
		t.Fatalf("state: %v", diags)
	}
	if got := data.PartialKeyHint; !got.Equal(types.StringValue("")) {
		t.Errorf("partial_key_hint = %v, want the planned \"\"", got)
	}
	for _, name := range []string{"id", "type"} {
		if got := objectString(t, data.CreatedBy, name); !got.Equal(types.StringValue("")) {
			t.Errorf("created_by.%s = %v, want the planned \"\"", name, got)
		}
	}
	if got := data.Name.ValueString(); got != "Renamed" {
		t.Errorf("name = %q, want Renamed", got)
	}
}

// planNewAttribute runs the schema plan modifiers of scope, principal or
// expires_at for an update over state, as Terraform plans a Computed attribute
// of a resource that changes (unknown before the modifiers), and returns the
// planned value.
func planNewAttribute(t *testing.T, s schema.Schema, state tfsdk.State, name string) attr.Value {
	t.Helper()
	ctx := context.Background()
	plan := tfsdk.Plan{Raw: state.Raw, Schema: s}
	switch a := s.Attributes[name].(type) {
	case schema.SingleNestedAttribute:
		attrTypes := a.GetType().(types.ObjectType).AttrTypes
		var stateValue types.Object
		if d := state.GetAttribute(ctx, path.Root(name), &stateValue); d.HasError() {
			t.Fatal(d)
		}
		req := planmodifier.ObjectRequest{
			Path: path.Root(name), State: state, Plan: plan,
			StateValue: stateValue, ConfigValue: types.ObjectNull(attrTypes), PlanValue: types.ObjectUnknown(attrTypes),
		}
		resp := planmodifier.ObjectResponse{PlanValue: req.PlanValue}
		for _, m := range a.PlanModifiers {
			m.PlanModifyObject(ctx, req, &resp)
			req.PlanValue = resp.PlanValue
		}
		if resp.Diagnostics.HasError() {
			t.Fatal(resp.Diagnostics)
		}
		return resp.PlanValue
	case schema.StringAttribute:
		var stateValue types.String
		if d := state.GetAttribute(ctx, path.Root(name), &stateValue); d.HasError() {
			t.Fatal(d)
		}
		req := planmodifier.StringRequest{
			Path: path.Root(name), State: state, Plan: plan,
			StateValue: stateValue, ConfigValue: types.StringNull(), PlanValue: types.StringUnknown(),
		}
		resp := planmodifier.StringResponse{PlanValue: req.PlanValue}
		for _, m := range a.PlanModifiers {
			m.PlanModifyString(ctx, req, &resp)
			req.PlanValue = resp.PlanValue
		}
		if resp.Diagnostics.HasError() {
			t.Fatal(resp.Diagnostics)
		}
		return resp.PlanValue
	default:
		t.Fatalf("%s is %T", name, s.Attributes[name])
		return nil
	}
}

// scope, principal and expires_at keep their prior value on an update, a real
// null included, so a key that never expires or has no principal shows no
// "(known after apply)". Over a state written before they existed (all three
// null, scope included, which the API always sends) they are planned unknown,
// not null: with the refresh skipped (-refresh=false) Update writes the API's
// values, and a planned null would make that an inconsistent result.
func TestAPIKeyResource_newAttributesPlan(t *testing.T) {
	r := &APIKeyResource{}
	s := schematest.ResourceSchema(t, r)
	objType := schematest.ResourceObjectType(t, r)
	base := func() map[string]tftypes.Value {
		vals := schematest.NullValues(t, r)
		vals["id"] = tftypes.NewValue(tftypes.String, "apikey_01ABC")
		vals["name"] = tftypes.NewValue(tftypes.String, "Key")
		vals["status"] = tftypes.NewValue(tftypes.String, "active")
		return vals
	}

	legacy := tfsdk.State{Raw: tftypes.NewValue(objType, base()), Schema: s}
	for _, name := range []string{"scope", "principal", "expires_at"} {
		t.Run("pre-upgrade state/"+name, func(t *testing.T) {
			if got := planNewAttribute(t, s, legacy, name); !got.IsUnknown() {
				t.Errorf("planned %v, want unknown", got)
			}
		})
	}

	current := base()
	scopeType := objType.AttributeTypes["scope"]
	current["scope"] = tftypes.NewValue(scopeType, map[string]tftypes.Value{
		"type":         tftypes.NewValue(tftypes.String, "workspace"),
		"workspace_id": tftypes.NewValue(tftypes.String, "wrkspc_01WS"),
	})
	refreshed := tfsdk.State{Raw: tftypes.NewValue(objType, current), Schema: s}
	for _, name := range []string{"principal", "expires_at"} {
		t.Run("real null kept/"+name, func(t *testing.T) {
			if got := planNewAttribute(t, s, refreshed, name); got.IsUnknown() || !got.IsNull() {
				t.Errorf("planned %v, want the stored null", got)
			}
		})
	}
	t.Run("value kept/scope", func(t *testing.T) {
		got, ok := planNewAttribute(t, s, refreshed, "scope").(types.Object)
		if !ok || got.IsUnknown() || got.IsNull() || got.Attributes()["workspace_id"].(types.String).ValueString() != "wrkspc_01WS" {
			t.Errorf("planned %v, want the stored scope", got)
		}
	})
}

// Read drives the resource end to end: an expired key keeps its read-only
// status in state, and the new attributes are populated.
func TestAPIKeyResource_ReadExpiredKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/organizations/api_keys/apikey_01ABC" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{
			"id": "apikey_01ABC", "name": "Expired", "status": "expired", "workspace_id": "wrkspc_01WS",
			"scope": {"type": "workspace", "workspace_id": "wrkspc_01WS"},
			"principal": {"type": "user_actor", "user_id": "user_01XYZ"},
			"expires_at": "2026-10-01T00:00:00Z", "created_at": "2026-01-01T00:00:00Z",
			"created_by": {"id": "user_01XYZ", "type": "user"}, "partial_key_hint": "abcd", "type": "api_key"
		}`)
	}))
	defer srv.Close()

	r := &APIKeyResource{adminClient: admintest.NewClient(t, srv)}
	s := schematest.ResourceSchema(t, r)
	objType := schematest.ResourceObjectType(t, r)
	vals := schematest.NullValues(t, r)
	vals["id"] = tftypes.NewValue(tftypes.String, "apikey_01ABC")
	state := tfsdk.State{Raw: tftypes.NewValue(objType, vals), Schema: s}

	resp := resource.ReadResponse{State: tfsdk.State{Schema: s}}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read returned errors: %v", resp.Diagnostics)
	}

	var data APIKeyResourceModel
	if diags := resp.State.Get(context.Background(), &data); diags.HasError() {
		t.Fatalf("state: %v", diags)
	}
	if got := data.Status.ValueString(); got != "expired" {
		t.Errorf("status = %q, want expired", got)
	}
	if got := data.ExpiresAt.ValueString(); got != "2026-10-01T00:00:00Z" {
		t.Errorf("expires_at = %q, want 2026-10-01T00:00:00Z", got)
	}
	if got := objectString(t, data.Scope, "workspace_id").ValueString(); got != "wrkspc_01WS" {
		t.Errorf("scope.workspace_id = %q, want wrkspc_01WS", got)
	}
}

// expired is read-only: the API sets it, and POST /v1/organizations/api_keys/{id}
// accepts only active, archived and inactive.
func TestAPIKeyResource_statusValidator(t *testing.T) {
	r := &APIKeyResource{}
	s := schematest.ResourceSchema(t, r)
	attrSchema, ok := s.Attributes["status"].(interface{ StringValidators() []validator.String })
	if !ok {
		t.Fatal("status attribute has no string validators")
	}

	for value, wantErr := range map[string]bool{
		"active":   false,
		"inactive": false,
		"archived": false,
		"expired":  true,
		"deleted":  true,
	} {
		var resp validator.StringResponse
		for _, v := range attrSchema.StringValidators() {
			v.ValidateString(context.Background(), validator.StringRequest{ConfigValue: types.StringValue(value)}, &resp)
		}
		if got := resp.Diagnostics.HasError(); got != wantErr {
			t.Errorf("status %q: error = %v, want %v", value, got, wantErr)
		}
	}
}

// apiKeyListItemAttrTypes must stay identical to the resource schema, because
// mapAPIKeyToListObject converts an APIKeyResourceModel with
// types.ObjectValueFrom.
func TestAPIKeyListItemAttrTypes_matchResourceSchema(t *testing.T) {
	objType := schematest.ResourceObjectType(t, &APIKeyResource{})
	if len(objType.AttributeTypes) != len(apiKeyListItemAttrTypes) {
		t.Fatalf("resource has %d attributes, list item has %d", len(objType.AttributeTypes), len(apiKeyListItemAttrTypes))
	}
	for name, want := range apiKeyListItemAttrTypes {
		got, ok := objType.AttributeTypes[name]
		if !ok {
			t.Errorf("list item attribute %q missing from the resource schema", name)
			continue
		}
		if !got.Equal(want.TerraformType(context.Background())) {
			t.Errorf("attribute %q: resource type %v, list item type %v", name, got, want)
		}
	}
}
