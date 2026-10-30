// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// The RBAC endpoints exist only for Claude Enterprise organizations and the
// test organization is a Console one, so CRUD wiring and mapping are covered
// here against httptest servers; the live tests in rbac_group_resource_test.go
// are opt-in.
package rbac

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/ippontech/terraform-provider-anthropic/internal/admintest"
	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
)

// rbacGroupFixture is the 2023-06-01 shape: both role_ids and roles.
const rbacGroupFixture = `{
	"type": "rbac_group",
	"id": "rbac_group_01UvWxYzAbCdEfGhIjKlMn",
	"name": "Engineering",
	"source_type": "direct",
	"role_ids": ["rbac_role_01CdEfGhIjKlMnOpQrStUv"],
	"roles": ["rbac_role_01CdEfGhIjKlMnOpQrStUv"],
	"created_at": "2026-07-09T18:00:00Z",
	"updated_at": "2026-07-09T18:00:00Z"
}`

const rbacGroupID = "rbac_group_01UvWxYzAbCdEfGhIjKlMn"

func groupState(t *testing.T, s tfsdk.State) rbacGroupModel {
	t.Helper()
	var m rbacGroupModel
	if diags := s.Get(context.Background(), &m); diags.HasError() {
		t.Fatalf("state: %v", diags)
	}
	return m
}

func TestRBACGroupResource_Create(t *testing.T) {
	t.Parallel()

	srv, rec := stub(t, func(recordedRequest) (int, string) { return http.StatusOK, rbacGroupFixture })
	r := &RBACGroupResource{client: admintest.NewClient(t, srv)}

	plan := resourcePlan(t, r, map[string]any{
		"name":        "Engineering",
		"id":          tftypes.UnknownValue,
		"source_type": tftypes.UnknownValue,
		"role_ids":    tftypes.UnknownValue,
		"created_at":  tftypes.UnknownValue,
		"updated_at":  tftypes.UnknownValue,
	})
	resp := resource.CreateResponse{State: emptyResourceState(t, r)}
	r.Create(context.Background(), resource.CreateRequest{Plan: plan}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create: %v", resp.Diagnostics)
	}

	reqs := rec.all()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	req := reqs[0]
	if req.Method != http.MethodPost || req.Path != "/v1/organizations/rbac_groups" {
		t.Errorf("request = %s %s, want POST /v1/organizations/rbac_groups", req.Method, req.Path)
	}
	assertBetaQuery(t, req)
	if len(req.Body) != 1 || req.Body["name"] != "Engineering" {
		t.Errorf("body = %v, want only name", req.Body)
	}

	got := groupState(t, resp.State)
	if got.ID.ValueString() != rbacGroupID || got.SourceType.ValueString() != "direct" {
		t.Errorf("id/source_type = %v/%v", got.ID, got.SourceType)
	}
	if ids := roleIDsOf(t, got); len(ids) != 1 || ids[0] != "rbac_role_01CdEfGhIjKlMnOpQrStUv" {
		t.Errorf("role_ids = %v", ids)
	}
	if got.CreatedAt.ValueString() != "2026-07-09T18:00:00Z" {
		t.Errorf("created_at = %v", got.CreatedAt)
	}
}

func TestRBACGroupResource_Read(t *testing.T) {
	t.Parallel()

	srv, rec := stub(t, func(recordedRequest) (int, string) { return http.StatusOK, rbacGroupFixture })
	r := &RBACGroupResource{client: admintest.NewClient(t, srv)}

	state := resourceState(t, r, map[string]any{"id": rbacGroupID, "name": "Old name"})
	resp := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", resp.Diagnostics)
	}

	req := rec.all()[0]
	if req.Method != http.MethodGet || req.Path != "/v1/organizations/rbac_groups/"+rbacGroupID {
		t.Errorf("request = %s %s", req.Method, req.Path)
	}
	assertBetaQuery(t, req)

	if got := groupState(t, resp.State); got.Name.ValueString() != "Engineering" {
		t.Errorf("name = %v, want the API value (drift)", got.Name)
	}
}

func TestRBACGroupResource_Read_notFoundRemovesFromState(t *testing.T) {
	t.Parallel()

	srv, _ := stub(t, func(recordedRequest) (int, string) { return http.StatusNotFound, notFoundBody })
	r := &RBACGroupResource{client: admintest.NewClient(t, srv)}

	state := resourceState(t, r, map[string]any{"id": rbacGroupID, "name": "Engineering"})
	resp := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Read: %v", resp.Diagnostics)
	}
	if !resp.State.Raw.IsNull() {
		t.Error("expected the resource to be removed from state after a 404")
	}
}

func TestRBACGroupResource_Read_otherErrorIsReported(t *testing.T) {
	t.Parallel()

	srv, _ := stub(t, func(recordedRequest) (int, string) {
		return http.StatusForbidden, `{"type":"error","error":{"type":"permission_error","message":"organization_type_not_supported"}}`
	})
	r := &RBACGroupResource{client: admintest.NewClient(t, srv)}

	state := resourceState(t, r, map[string]any{"id": rbacGroupID, "name": "Engineering"})
	resp := resource.ReadResponse{State: state}
	r.Read(context.Background(), resource.ReadRequest{State: state}, &resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected a diagnostic for a 403")
	}
}

func TestRBACGroupResource_Update(t *testing.T) {
	t.Parallel()

	srv, rec := stub(t, func(recordedRequest) (int, string) {
		return http.StatusOK, `{"id":"` + rbacGroupID + `","name":"Platform","source_type":"direct","role_ids":[],"created_at":"2026-07-09T18:00:00Z","updated_at":"2026-07-10T09:00:00Z"}`
	})
	r := &RBACGroupResource{client: admintest.NewClient(t, srv)}

	state := resourceState(t, r, map[string]any{"id": rbacGroupID, "name": "Engineering", "source_type": "direct"})
	plan := resourcePlan(t, r, map[string]any{
		"id":          rbacGroupID,
		"name":        "Platform",
		"source_type": "direct",
		"role_ids":    tftypes.UnknownValue,
		"created_at":  "2026-07-09T18:00:00Z",
		"updated_at":  tftypes.UnknownValue,
	})
	resp := resource.UpdateResponse{State: state}
	r.Update(context.Background(), resource.UpdateRequest{Plan: plan, State: state}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Update: %v", resp.Diagnostics)
	}

	req := rec.all()[0]
	if req.Method != http.MethodPost || req.Path != "/v1/organizations/rbac_groups/"+rbacGroupID {
		t.Errorf("request = %s %s, want POST on the group", req.Method, req.Path)
	}
	assertBetaQuery(t, req)
	if len(req.Body) != 1 || req.Body["name"] != "Platform" {
		t.Errorf("body = %v, want only name", req.Body)
	}

	got := groupState(t, resp.State)
	if got.Name.ValueString() != "Platform" || got.UpdatedAt.ValueString() != "2026-07-10T09:00:00Z" {
		t.Errorf("name/updated_at = %v/%v", got.Name, got.UpdatedAt)
	}
	if got.RoleIDs.IsNull() || len(got.RoleIDs.Elements()) != 0 {
		t.Errorf("role_ids = %v, want an empty set", got.RoleIDs)
	}
}

func TestRBACGroupResource_Delete(t *testing.T) {
	t.Parallel()

	for name, status := range map[string]int{"deleted": http.StatusOK, "already gone": http.StatusNotFound} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv, rec := stub(t, func(recordedRequest) (int, string) {
				if status == http.StatusNotFound {
					return status, notFoundBody
				}
				return status, `{"id":"` + rbacGroupID + `","type":"rbac_group_deleted"}`
			})
			r := &RBACGroupResource{client: admintest.NewClient(t, srv)}

			state := resourceState(t, r, map[string]any{"id": rbacGroupID, "name": "Engineering"})
			resp := resource.DeleteResponse{State: state}
			r.Delete(context.Background(), resource.DeleteRequest{State: state}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("Delete: %v", resp.Diagnostics)
			}
			req := rec.all()[0]
			if req.Method != http.MethodDelete || req.Path != "/v1/organizations/rbac_groups/"+rbacGroupID {
				t.Errorf("request = %s %s", req.Method, req.Path)
			}
			assertBetaQuery(t, req)
		})
	}
}

func TestRBACGroupResource_ImportState(t *testing.T) {
	t.Parallel()

	r := &RBACGroupResource{}
	resp := resource.ImportStateResponse{State: schematest.NullState(t, r)}
	r.ImportState(context.Background(), resource.ImportStateRequest{ID: rbacGroupID}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("ImportState: %v", resp.Diagnostics)
	}
	var id string
	resp.State.GetAttribute(context.Background(), path.Root("id"), &id)
	if id != rbacGroupID {
		t.Errorf("id = %q", id)
	}
}

// --- ModifyPlan: SCIM groups ---

func runModifyPlan(t *testing.T, sourceType string, planName *string) resource.ModifyPlanResponse {
	t.Helper()
	r := &RBACGroupResource{}
	state := resourceState(t, r, map[string]any{"id": rbacGroupID, "name": "Engineering", "source_type": sourceType})

	plan := tfsdk.Plan{Raw: tftypes.NewValue(schematest.ResourceObjectType(t, r), nil), Schema: schematest.ResourceSchema(t, r)}
	if planName != nil {
		plan = resourcePlan(t, r, map[string]any{"id": rbacGroupID, "name": *planName, "source_type": sourceType})
	}
	resp := resource.ModifyPlanResponse{Plan: plan}
	r.ModifyPlan(context.Background(), resource.ModifyPlanRequest{State: state, Plan: plan}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("ModifyPlan must never error: %v", resp.Diagnostics)
	}
	return resp
}

func TestRBACGroupResource_ModifyPlan(t *testing.T) {
	t.Parallel()

	rename := "Platform"
	same := "Engineering"

	cases := []struct {
		name       string
		sourceType string
		planName   *string
		wantWarn   bool
	}{
		{"rename a scim group", sourceTypeSCIM, &rename, true},
		{"destroy a scim group", sourceTypeSCIM, nil, true},
		{"no change on a scim group", sourceTypeSCIM, &same, false},
		{"rename a direct group", "direct", &rename, false},
		{"destroy a direct group", "direct", nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			resp := runModifyPlan(t, tc.sourceType, tc.planName)
			if got := resp.Diagnostics.WarningsCount() > 0; got != tc.wantWarn {
				t.Errorf("warning = %v, want %v (%v)", got, tc.wantWarn, resp.Diagnostics)
			}
		})
	}
}

func TestRBACGroupResource_ModifyPlan_create(t *testing.T) {
	t.Parallel()

	r := &RBACGroupResource{}
	plan := resourcePlan(t, r, map[string]any{"name": "Engineering"})
	state := tfsdk.State{Raw: tftypes.NewValue(schematest.ResourceObjectType(t, r), nil), Schema: schematest.ResourceSchema(t, r)}
	resp := resource.ModifyPlanResponse{Plan: plan}
	r.ModifyPlan(context.Background(), resource.ModifyPlanRequest{State: state, Plan: plan}, &resp)
	if len(resp.Diagnostics) != 0 {
		t.Errorf("create plan: %v", resp.Diagnostics)
	}
}

// The reference sets the name at 1 to 255 characters (minLength/maxLength,
// which count characters), as the schema says. A byte count rejected a
// 100-character Japanese name (300 bytes) at plan time.
func TestRBACGroupResource_nameLengthCountsCharacters(t *testing.T) {
	t.Parallel()

	attr := schematest.ResourceSchema(t, &RBACGroupResource{}).Attributes["name"].(schema.StringAttribute)
	validate := func(name string) bool {
		var diags diag.Diagnostics
		for _, v := range attr.Validators {
			var resp validator.StringResponse
			v.ValidateString(context.Background(), validator.StringRequest{Path: path.Root("name"), ConfigValue: types.StringValue(name)}, &resp)
			diags.Append(resp.Diagnostics...)
		}
		return !diags.HasError()
	}

	for name, want := range map[string]bool{
		strings.Repeat("営", 100): true, // 300 bytes
		strings.Repeat("é", 255): true, // 510 bytes
		strings.Repeat("a", 255): true,
		strings.Repeat("é", 256): false,
		"":                       false,
	} {
		if got := validate(name); got != want {
			t.Errorf("name of %d characters (%d bytes): valid = %v, want %v", utf8.RuneCountInString(name), len(name), got, want)
		}
	}
}
