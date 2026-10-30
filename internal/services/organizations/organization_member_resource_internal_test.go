// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Live create/destroy CRUD acceptance tests for anthropic_organization_member
// are intentionally not written: removing an org member is irreversible and
// there is no dedicated test organization yet (Admin API resource-test
// blocker tracked in #58, see CLAUDE.md). Coverage here is httptest-based
// unit tests only.
package organizations

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/ippontech/terraform-provider-anthropic/internal/admintest"
	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
)

const organizationMemberResourceFixture = `{
	"id": "user_01WCz1FkmYMm4gnmykNKUu3Q",
	"email": "user@example.com",
	"name": "Example User",
	"role": "developer",
	"added_at": "2026-01-01T00:00:00Z",
	"type": "user"
}`

// --- Create ---

func TestOrganizationMemberResource_CreateAdoptsExistingMember(t *testing.T) {
	t.Parallel()

	var gotMethods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotMethods = append(gotMethods, req.Method)
		if req.URL.Path != "/v1/organizations/users/user_01WCz1FkmYMm4gnmykNKUu3Q" {
			t.Errorf("unexpected path: %s", req.URL.Path)
		}
		if req.Method != http.MethodGet {
			t.Errorf("unexpected method: %s", req.Method)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, organizationMemberResourceFixture)
	}))
	defer srv.Close()

	r := &OrganizationMemberResource{client: admintest.NewClient(t, srv)}
	schema := schematest.ResourceSchema(t, r)
	objType := schematest.ResourceObjectType(t, r)
	vals := schematest.NullValues(t, r)
	vals["id"] = tftypes.NewValue(objType.AttributeTypes["id"], "user_01WCz1FkmYMm4gnmykNKUu3Q")
	vals["role"] = tftypes.NewValue(objType.AttributeTypes["role"], "developer")
	plan := tfsdk.Plan{Raw: tftypes.NewValue(objType, vals), Schema: schema}

	var resp resource.CreateResponse
	resp.State = tfsdk.State{Schema: schema}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &resp)

	if !resp.Diagnostics.HasError() {
		var data OrganizationMemberResourceModel
		if diags := resp.State.Get(context.Background(), &data); diags.HasError() {
			t.Fatalf("failed to read created state: %v", diags)
		}
		if data.ID.ValueString() != "user_01WCz1FkmYMm4gnmykNKUu3Q" || data.Role.ValueString() != "developer" {
			t.Errorf("unexpected adopted member state: id=%q role=%q", data.ID.ValueString(), data.Role.ValueString())
		}
	} else {
		t.Fatalf("Create returned errors: %v", resp.Diagnostics)
	}
	if len(gotMethods) != 1 || gotMethods[0] != http.MethodGet {
		t.Errorf("methods = %v, want only GET", gotMethods)
	}
	if resp.Diagnostics.WarningsCount() != 0 {
		t.Errorf("unexpected adoption warnings: %v", resp.Diagnostics)
	}
}

func TestOrganizationMemberResource_CreateRejectsRoleMismatch(t *testing.T) {
	t.Parallel()

	var gotMethods []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotMethods = append(gotMethods, req.Method)
		if req.URL.Path != "/v1/organizations/users/user_01WCz1FkmYMm4gnmykNKUu3Q" {
			t.Errorf("unexpected path: %s", req.URL.Path)
		}
		if req.Method != http.MethodGet {
			t.Errorf("unexpected method: %s", req.Method)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, organizationMemberResourceFixture)
	}))
	defer srv.Close()

	r := &OrganizationMemberResource{client: admintest.NewClient(t, srv)}
	schema := schematest.ResourceSchema(t, r)
	objType := schematest.ResourceObjectType(t, r)
	vals := schematest.NullValues(t, r)
	vals["id"] = tftypes.NewValue(objType.AttributeTypes["id"], "user_01WCz1FkmYMm4gnmykNKUu3Q")
	vals["role"] = tftypes.NewValue(objType.AttributeTypes["role"], "user")
	plan := tfsdk.Plan{Raw: tftypes.NewValue(objType, vals), Schema: schema}

	resp := resource.CreateResponse{State: tfsdk.State{Raw: tftypes.NewValue(objType, nil), Schema: schema}}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &resp)
	if resp.Diagnostics.ErrorsCount() != 1 {
		t.Fatalf("expected one role-mismatch error, got: %v", resp.Diagnostics)
	}
	if got := resp.Diagnostics[0].Summary(); got != "Organization Member Role Mismatch" {
		t.Errorf("error summary = %q, want Organization Member Role Mismatch", got)
	}
	for _, detail := range []string{"user_01WCz1FkmYMm4gnmykNKUu3Q", `has role "developer"`, `configured role is "user"`, "terraform import", "terraform plan"} {
		if !strings.Contains(resp.Diagnostics[0].Detail(), detail) {
			t.Errorf("error detail = %q, want it to contain %q", resp.Diagnostics[0].Detail(), detail)
		}
	}
	if len(gotMethods) != 1 || gotMethods[0] != http.MethodGet {
		t.Errorf("methods = %v, want only GET", gotMethods)
	}
	if !resp.State.Raw.IsNull() {
		t.Fatal("expected rejected adoption to leave state null")
	}
}

func TestOrganizationMemberResource_CreateRequiresExistingMember(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method != http.MethodGet {
			t.Errorf("unexpected method: %s", req.Method)
		}
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"type":"not_found_error","message":"user not found"}}`)
	}))
	defer srv.Close()

	r := &OrganizationMemberResource{client: admintest.NewClient(t, srv)}
	schema := schematest.ResourceSchema(t, r)
	objType := schematest.ResourceObjectType(t, r)
	vals := schematest.NullValues(t, r)
	vals["id"] = tftypes.NewValue(objType.AttributeTypes["id"], "user_missing")
	vals["role"] = tftypes.NewValue(objType.AttributeTypes["role"], "developer")
	plan := tfsdk.Plan{Raw: tftypes.NewValue(objType, vals), Schema: schema}

	var resp resource.CreateResponse
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected Create to report that the member does not exist")
	}
	if got := resp.Diagnostics[0].Summary(); got != "Organization Member Not Found" {
		t.Errorf("diagnostic summary = %q, want Organization Member Not Found", got)
	}
}

// --- Read ---

func TestOrganizationMemberResource_Read(t *testing.T) {
	t.Parallel()

	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotMethod = req.Method
		gotPath = req.URL.Path
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, organizationMemberResourceFixture)
	}))
	defer srv.Close()

	r := &OrganizationMemberResource{client: admintest.NewClient(t, srv)}

	schema := schematest.ResourceSchema(t, r)
	objType := schematest.ResourceObjectType(t, r)
	vals := schematest.NullValues(t, r)
	vals["id"] = tftypes.NewValue(objType.AttributeTypes["id"], "user_01WCz1FkmYMm4gnmykNKUu3Q")

	state := tfsdk.State{
		Raw:    tftypes.NewValue(objType, vals),
		Schema: schema,
	}

	var resp resource.ReadResponse
	resp.State = tfsdk.State{Schema: schema}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read returned errors: %v", resp.Diagnostics)
	}

	if gotMethod != http.MethodGet {
		t.Errorf("expected GET, got %s", gotMethod)
	}
	if gotPath != "/v1/organizations/users/user_01WCz1FkmYMm4gnmykNKUu3Q" {
		t.Errorf("expected users read path, got %s", gotPath)
	}

	var data OrganizationMemberResourceModel
	if diags := resp.State.Get(context.Background(), &data); diags.HasError() {
		t.Fatalf("failed to read state: %v", diags)
	}
	if data.Email.ValueString() != "user@example.com" {
		t.Errorf("expected email to be mapped from response, got %s", data.Email.ValueString())
	}
	if data.Role.ValueString() != "developer" {
		t.Errorf("expected role to be mapped from response, got %s", data.Role.ValueString())
	}
}

func TestOrganizationMemberResource_Read_notFoundRemovesFromState(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, `{"error":{"type":"not_found_error","message":"user not found"}}`)
	}))
	defer srv.Close()

	r := &OrganizationMemberResource{client: admintest.NewClient(t, srv)}

	schema := schematest.ResourceSchema(t, r)
	objType := schematest.ResourceObjectType(t, r)
	vals := schematest.NullValues(t, r)
	vals["id"] = tftypes.NewValue(objType.AttributeTypes["id"], "user_01WCz1FkmYMm4gnmykNKUu3Q")

	state := tfsdk.State{
		Raw:    tftypes.NewValue(objType, vals),
		Schema: schema,
	}

	var resp resource.ReadResponse
	resp.State = state
	r.Read(context.Background(), resource.ReadRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read returned errors: %v", resp.Diagnostics)
	}

	if !resp.State.Raw.IsNull() {
		t.Error("expected state to be removed (null) after a 404, but it was not")
	}
}

func TestOrganizationMemberResource_GetMemberErrors(t *testing.T) {
	for _, test := range []struct {
		name         string
		responseCode int
		body         string
		wantDetail   string
	}{
		{name: "malformed response", responseCode: http.StatusOK, body: `{`, wantDetail: "unable to parse organization member response"},
		{name: "forbidden", responseCode: http.StatusForbidden, body: `{"error":{"message":"access denied"}}`, wantDetail: "access denied"},
		{name: "revoked key", responseCode: http.StatusUnauthorized, body: `{"error":{"message":"invalid key"}}`, wantDetail: "invalid key"},
		{name: "server error", responseCode: http.StatusInternalServerError, body: `{"error":{"message":"server unavailable"}}`, wantDetail: "server unavailable"},
	} {
		for _, operation := range []string{"create", "read"} {
			t.Run(operation+"/"+test.name, func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					w.WriteHeader(test.responseCode)
					_, _ = io.WriteString(w, test.body)
				}))
				defer srv.Close()
				member := &OrganizationMemberResource{client: admintest.NewClient(t, srv)}
				ctx := context.Background()
				state := schematest.NullState(t, member)
				data := mapOrganizationMemberToResourceState(organizationMemberAPIResponse{ID: "user_example", Role: "developer"}, types.BoolValue(false))
				if diags := state.Set(ctx, data); diags.HasError() {
					t.Fatalf("State.Set: %v", diags)
				}
				if operation == "create" {
					resp := resource.CreateResponse{State: tfsdk.State{Raw: tftypes.NewValue(schematest.ResourceObjectType(t, member), nil), Schema: state.Schema}}
					member.Create(ctx, resource.CreateRequest{Plan: tfsdk.Plan(state)}, &resp)
					if !resp.Diagnostics.HasError() || !strings.HasPrefix(resp.Diagnostics[0].Detail(), "Unable to read organization member: ") || !strings.Contains(resp.Diagnostics[0].Detail(), test.wantDetail) {
						t.Fatalf("expected %q error, got %v", test.wantDetail, resp.Diagnostics)
					}
					if !resp.State.Raw.IsNull() {
						t.Fatal("failed lookup must not adopt the member")
					}
				} else {
					resp := resource.ReadResponse{State: state}
					member.Read(ctx, resource.ReadRequest{State: state}, &resp)
					if !resp.Diagnostics.HasError() || !strings.HasPrefix(resp.Diagnostics[0].Detail(), "Unable to read organization member: ") || !strings.Contains(resp.Diagnostics[0].Detail(), test.wantDetail) {
						t.Fatalf("expected %q error, got %v", test.wantDetail, resp.Diagnostics)
					}
					if !resp.State.Raw.Equal(state.Raw) {
						t.Fatal("failed lookup must preserve existing state")
					}
				}
			})
		}
	}
}

// --- Update ---

func TestOrganizationMemberResource_Update(t *testing.T) {
	t.Parallel()

	var gotMethod, gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotMethod = req.Method
		gotPath = req.URL.Path
		_ = json.NewDecoder(req.Body).Decode(&gotBody)
		w.Header().Set("content-type", "application/json")
		_, _ = io.WriteString(w, organizationMemberResourceFixture)
	}))
	defer srv.Close()

	r := &OrganizationMemberResource{client: admintest.NewClient(t, srv)}

	schema := schematest.ResourceSchema(t, r)
	objType := schematest.ResourceObjectType(t, r)
	vals := schematest.NullValues(t, r)
	vals["id"] = tftypes.NewValue(objType.AttributeTypes["id"], "user_01WCz1FkmYMm4gnmykNKUu3Q")
	vals["role"] = tftypes.NewValue(objType.AttributeTypes["role"], "developer")

	plan := tfsdk.Plan{
		Raw:    tftypes.NewValue(objType, vals),
		Schema: schema,
	}

	var resp resource.UpdateResponse
	resp.State = tfsdk.State{Schema: schema}
	r.Update(context.Background(), resource.UpdateRequest{Plan: plan}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Update returned errors: %v", resp.Diagnostics)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("expected POST, got %s", gotMethod)
	}
	if gotPath != "/v1/organizations/users/user_01WCz1FkmYMm4gnmykNKUu3Q" {
		t.Errorf("expected users update path, got %s", gotPath)
	}
	if gotBody["role"] != "developer" {
		t.Errorf("expected role 'developer' in request body, got %v", gotBody["role"])
	}

	var data OrganizationMemberResourceModel
	if diags := resp.State.Get(context.Background(), &data); diags.HasError() {
		t.Fatalf("failed to read state: %v", diags)
	}
	if data.Email.ValueString() != "user@example.com" {
		t.Errorf("expected email to be mapped from response, got %s", data.Email.ValueString())
	}
	if data.Role.ValueString() != "developer" {
		t.Errorf("expected role to be mapped from response, got %s", data.Role.ValueString())
	}
}

func TestOrganizationMemberResource_RemoveOnDestroySurvivesLifecycle(t *testing.T) {
	for _, value := range []types.Bool{types.BoolValue(true), types.BoolValue(false), types.BoolNull()} {
		for _, operation := range []string{"create", "read", "update"} {
			t.Run(operation+"/"+value.String(), func(t *testing.T) {
				srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
					w.Header().Set("content-type", "application/json")
					_, _ = io.WriteString(w, organizationMemberResourceFixture)
				}))
				defer srv.Close()
				member := &OrganizationMemberResource{client: admintest.NewClient(t, srv)}
				ctx := context.Background()
				state := schematest.NullState(t, member)
				data := mapOrganizationMemberToResourceState(organizationMemberAPIResponse{
					ID: "user_01WCz1FkmYMm4gnmykNKUu3Q", Role: "developer",
				}, value)
				data.RemoveOnDestroy = value
				if diags := state.Set(ctx, data); diags.HasError() {
					t.Fatalf("State.Set: %v", diags)
				}
				plan := tfsdk.Plan(state)
				switch operation {
				case "create":
					resp := resource.CreateResponse{State: state}
					member.Create(ctx, resource.CreateRequest{Plan: plan}, &resp)
					if resp.Diagnostics.HasError() {
						t.Fatalf("Create: %v", resp.Diagnostics)
					}
					state = resp.State
				case "read":
					resp := resource.ReadResponse{State: state}
					member.Read(ctx, resource.ReadRequest{State: state}, &resp)
					if resp.Diagnostics.HasError() {
						t.Fatalf("Read: %v", resp.Diagnostics)
					}
					state = resp.State
				case "update":
					resp := resource.UpdateResponse{State: state}
					member.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, &resp)
					if resp.Diagnostics.HasError() {
						t.Fatalf("Update: %v", resp.Diagnostics)
					}
					state = resp.State
				}
				var got types.Bool
				if diags := state.GetAttribute(ctx, path.Root("remove_on_destroy"), &got); diags.HasError() {
					t.Fatalf("State.GetAttribute: %v", diags)
				}
				if !got.Equal(types.BoolValue(value.ValueBool())) {
					t.Errorf("remove_on_destroy = %v, want %v", got, value.ValueBool())
				}
			})
		}
	}
}

// --- Delete ---

func TestOrganizationMemberResource_Delete(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name            string
		removeOnDestroy any
		responseCode    int
		wantRequests    int
		wantError       bool
	}{
		{name: "disabled", removeOnDestroy: false, responseCode: http.StatusNoContent, wantError: true},
		{name: "null", removeOnDestroy: nil, responseCode: http.StatusNoContent, wantError: true},
		{name: "unknown", removeOnDestroy: tftypes.UnknownValue, responseCode: http.StatusNoContent, wantError: true},
		{name: "enabled", removeOnDestroy: true, responseCode: http.StatusNoContent, wantRequests: 1},
		{name: "already gone", removeOnDestroy: true, responseCode: http.StatusNotFound, wantRequests: 1},
		{name: "forbidden", removeOnDestroy: true, responseCode: http.StatusForbidden, wantRequests: 1, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				requests++
				if req.Method != http.MethodDelete || req.URL.Path != "/v1/organizations/users/user_01WCz1FkmYMm4gnmykNKUu3Q" {
					t.Errorf("unexpected request: %s %s", req.Method, req.URL)
				}
				w.WriteHeader(test.responseCode)
			}))
			defer srv.Close()

			member := &OrganizationMemberResource{client: admintest.NewClient(t, srv)}
			objType := schematest.ResourceObjectType(t, member)
			vals := schematest.NullValues(t, member)
			vals["id"] = tftypes.NewValue(objType.AttributeTypes["id"], "user_01WCz1FkmYMm4gnmykNKUu3Q")
			vals["remove_on_destroy"] = tftypes.NewValue(objType.AttributeTypes["remove_on_destroy"], test.removeOnDestroy)
			state := tfsdk.State{Raw: tftypes.NewValue(objType, vals), Schema: schematest.ResourceSchema(t, member)}
			resp := resource.DeleteResponse{State: state}
			member.Delete(context.Background(), resource.DeleteRequest{State: state}, &resp)
			if resp.Diagnostics.HasError() != test.wantError {
				t.Fatalf("Delete diagnostics = %v, wantError = %v", resp.Diagnostics, test.wantError)
			}
			if test.wantRequests == 0 {
				if got := resp.Diagnostics[0].Summary(); got != "Organization Member Removal Disabled" {
					t.Errorf("diagnostic summary = %q, want Organization Member Removal Disabled", got)
				}
				if !strings.Contains(resp.Diagnostics[0].Detail(), "remove_on_destroy = true") || !strings.Contains(resp.Diagnostics[0].Detail(), "terraform apply") {
					t.Errorf("diagnostic lacks enable-and-apply guidance: %s", resp.Diagnostics[0].Detail())
				}
				if !resp.State.Raw.Equal(state.Raw) {
					t.Fatal("blocked destroy must preserve state")
				}
			}
			if requests != test.wantRequests {
				t.Errorf("requests = %d, want %d", requests, test.wantRequests)
			}
		})
	}
}

// --- ImportState ---

func TestOrganizationMemberResource_ImportState(t *testing.T) {
	t.Parallel()

	r := &OrganizationMemberResource{}
	state := schematest.NullState(t, r)

	var resp resource.ImportStateResponse
	resp.State = state
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: "user_01WCz1FkmYMm4gnmykNKUu3Q"}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("ImportState returned errors: %v", resp.Diagnostics)
	}

	var id string
	if diags := resp.State.GetAttribute(context.Background(), path.Root("id"), &id); diags.HasError() {
		t.Fatalf("failed to read imported id: %v", diags)
	}
	if id != "user_01WCz1FkmYMm4gnmykNKUu3Q" {
		t.Errorf("expected imported id to be 'user_01WCz1FkmYMm4gnmykNKUu3Q', got %s", id)
	}
}

// --- Schema ---

// A member, imported or adopted on create, can hold admin or an Enterprise
// administrative role, so the role attribute accepts every organization role
// (assertRoleAttribute is shared with the invite resource test).
func TestOrganizationMemberResource_roleAttribute(t *testing.T) {
	t.Parallel()

	assertRoleAttribute(t, schematest.ResourceSchema(t, &OrganizationMemberResource{}))
}

// --- mapOrganizationMemberToResourceState ---

func TestMapOrganizationMemberToResourceState(t *testing.T) {
	t.Parallel()

	var member organizationMemberAPIResponse
	if err := json.Unmarshal([]byte(organizationMemberResourceFixture), &member); err != nil {
		t.Fatalf("failed to unmarshal fixture: %s", err)
	}

	data := mapOrganizationMemberToResourceState(member, types.BoolValue(false))

	if data.ID.ValueString() != "user_01WCz1FkmYMm4gnmykNKUu3Q" {
		t.Errorf("expected id to be mapped, got %s", data.ID.ValueString())
	}
	if data.Email.ValueString() != "user@example.com" {
		t.Errorf("expected email to be mapped, got %s", data.Email.ValueString())
	}
	if data.Name.ValueString() != "Example User" {
		t.Errorf("expected name to be mapped, got %s", data.Name.ValueString())
	}
	if data.Role.ValueString() != "developer" {
		t.Errorf("expected role to be mapped, got %s", data.Role.ValueString())
	}
	if data.AddedAt.ValueString() != "2026-01-01T00:00:00Z" {
		t.Errorf("expected added_at to be mapped, got %s", data.AddedAt.ValueString())
	}
	if data.Type.ValueString() != "user" {
		t.Errorf("expected type to be mapped, got %s", data.Type.ValueString())
	}
}

func TestMapOrganizationMemberToResourceState_RemoveOnDestroy(t *testing.T) {
	for _, value := range []types.Bool{types.BoolValue(true), types.BoolValue(false), types.BoolNull(), types.BoolUnknown()} {
		t.Run(value.String(), func(t *testing.T) {
			data := mapOrganizationMemberToResourceState(organizationMemberAPIResponse{}, value)
			if data.RemoveOnDestroy.IsNull() || data.RemoveOnDestroy.IsUnknown() || data.RemoveOnDestroy.ValueBool() != value.ValueBool() {
				t.Errorf("remove_on_destroy = %v, want %v", data.RemoveOnDestroy, value.ValueBool())
			}
		})
	}
}
