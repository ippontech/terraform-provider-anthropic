// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/ippontech/terraform-provider-anthropic/internal/admintest"
	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
)

func inviteFixture(status string) string {
	return `{
		"id": "invite_01ABC",
		"email": "new-teammate@example.com",
		"role": "developer",
		"status": "` + status + `",
		"invited_at": "2026-01-01T00:00:00Z",
		"expires_at": "2026-01-08T00:00:00Z",
		"accepted_at": "",
		"type": "invite"
	}`
}

func TestInviteCreate_mapsResponseToState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/organizations/invites" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, inviteFixture("pending"))
	}))
	defer srv.Close()

	client := admintest.NewClient(t, srv)
	body, err := client.DoRequest(context.Background(), "POST", "/v1/organizations/invites", inviteCreateRequest{
		Email: "new-teammate@example.com",
		Role:  "developer",
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	var inv inviteAPIResponse
	if err := json.Unmarshal(body, &inv); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	var data InviteResourceModel
	mapInviteToState(&inv, &data)

	if data.ID.ValueString() != "invite_01ABC" {
		t.Errorf("ID = %q, want invite_01ABC", data.ID.ValueString())
	}
	if data.Email.ValueString() != "new-teammate@example.com" {
		t.Errorf("Email = %q, want new-teammate@example.com", data.Email.ValueString())
	}
	if data.Role.ValueString() != "developer" {
		t.Errorf("Role = %q, want developer", data.Role.ValueString())
	}
	if data.Status.ValueString() != "pending" {
		t.Errorf("Status = %q, want pending", data.Status.ValueString())
	}
	if data.Type.ValueString() != "invite" {
		t.Errorf("Type = %q, want invite", data.Type.ValueString())
	}
}

func TestInviteRead_handlesReturnedStatus(t *testing.T) {
	for _, test := range []struct {
		name         string
		status       string
		responseCode int
		wantGone     bool
	}{
		{name: "pending", status: "pending", responseCode: http.StatusOK},
		{name: "accepted", status: "accepted", responseCode: http.StatusOK},
		{name: "expired", status: "expired", responseCode: http.StatusOK},
		{name: "deleted", status: "deleted", responseCode: http.StatusOK, wantGone: true},
		{name: "not found", responseCode: http.StatusNotFound, wantGone: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet || r.URL.Path != "/v1/organizations/invites/invite_01ABC" || r.URL.RawQuery != "beta=true" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				w.WriteHeader(test.responseCode)
				if test.responseCode == http.StatusOK {
					_, _ = io.WriteString(w, inviteFixture(test.status))
				}
			}))
			defer srv.Close()

			r := &InviteResource{adminClient: admintest.NewClient(t, srv)}
			schema := schematest.ResourceSchema(t, r)
			objType := schematest.ResourceObjectType(t, r)
			vals := schematest.NullValues(t, r)
			vals["id"] = tftypes.NewValue(objType.AttributeTypes["id"], "invite_01ABC")
			state := tfsdk.State{Raw: tftypes.NewValue(objType, vals), Schema: schema}

			resp := resource.ReadResponse{State: state}
			r.Read(context.Background(), resource.ReadRequest{State: state}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("Read returned errors: %v", resp.Diagnostics)
			}
			if test.wantGone {
				if !resp.State.Raw.IsNull() {
					t.Fatal("expected invite to be removed from state")
				}
				return
			}
			if resp.State.Raw.IsNull() {
				t.Fatal("expected invite to remain in state")
			}

			var got InviteResourceModel
			if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
				t.Fatalf("failed to read state: %v", diags)
			}
			if got.Status.ValueString() != test.status {
				t.Errorf("Status = %q, want %q", got.Status.ValueString(), test.status)
			}
		})
	}
}

func TestInviteDelete(t *testing.T) {
	for _, test := range []struct {
		name         string
		status       any
		responseCode int
		wantRequests int
		wantError    bool
	}{
		{name: "pending", status: "pending", responseCode: http.StatusNoContent, wantRequests: 1},
		{name: "already gone", status: "pending", responseCode: http.StatusNotFound, wantRequests: 1},
		{name: "bad request", status: "pending", responseCode: http.StatusBadRequest, wantRequests: 1, wantError: true},
		{name: "conflict", status: "pending", responseCode: http.StatusConflict, wantRequests: 1, wantError: true},
		{name: "accepted", status: "accepted", responseCode: http.StatusBadRequest},
		{name: "expired", status: "expired", responseCode: http.StatusConflict},
		{name: "deleted", status: "deleted", responseCode: http.StatusNotFound},
		{name: "null status", status: nil, responseCode: http.StatusNoContent, wantRequests: 1},
		{name: "unknown status", status: tftypes.UnknownValue, responseCode: http.StatusNoContent, wantRequests: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			var requests int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
				requests++
				if req.Method != http.MethodDelete || req.URL.Path != "/v1/organizations/invites/invite_01ABC" || req.URL.RawQuery != "beta=true" {
					t.Errorf("unexpected request: %s %s", req.Method, req.URL)
				}
				w.WriteHeader(test.responseCode)
			}))
			defer srv.Close()

			invite := &InviteResource{adminClient: admintest.NewClient(t, srv)}
			state := schematest.NullState(t, invite)
			objType := schematest.ResourceObjectType(t, invite)
			vals := schematest.NullValues(t, invite)
			vals["id"] = tftypes.NewValue(objType.AttributeTypes["id"], "invite_01ABC")
			vals["status"] = tftypes.NewValue(objType.AttributeTypes["status"], test.status)
			state.Raw = tftypes.NewValue(objType, vals)

			resp := resource.DeleteResponse{State: state}
			invite.Delete(context.Background(), resource.DeleteRequest{State: state}, &resp)
			if resp.Diagnostics.HasError() != test.wantError {
				t.Fatalf("Delete diagnostics = %v, wantError = %v", resp.Diagnostics, test.wantError)
			}
			if requests != test.wantRequests {
				t.Errorf("requests = %d, want %d", requests, test.wantRequests)
			}
		})
	}
}

// TestInviteResource_planModifiers verifies email and role are wired with
// RequiresReplace, since the API has no update endpoint for invites.
func TestInviteResource_planModifiers(t *testing.T) {
	r := NewInviteResource()
	s := schematest.ResourceSchema(t, r)

	for _, name := range []string{"email", "role"} {
		attr, ok := s.Attributes[name]
		if !ok {
			t.Fatalf("attribute %q not found in schema", name)
		}
		strAttr, ok := attr.(schema.StringAttribute)
		if !ok {
			t.Fatalf("attribute %q is not a StringAttribute", name)
		}

		found := false
		for _, pm := range strAttr.PlanModifiers {
			if pm.Description(context.Background()) == stringplanmodifier.RequiresReplace().Description(context.Background()) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("attribute %q missing RequiresReplace plan modifier", name)
		}
	}
}

// allOrganizationRoles is every value the API reports as an organization role
// (OrganizationRole in the Admin API reference), assignable or not.
var allOrganizationRoles = []string{
	"user", "developer", "billing", "claude_code_user", "admin",
	"managed", "owner", "membership_admin", "primary_owner",
}

// assertRoleAttribute checks that a role attribute only rejects an empty value
// (no OneOf: an imported invite or member can hold a role the API cannot
// assign) and that its description names the roles the API cannot assign.
func assertRoleAttribute(t *testing.T, s schema.Schema) {
	t.Helper()
	role, ok := s.Attributes["role"].(schema.StringAttribute)
	if !ok {
		t.Fatalf("role is %T, want schema.StringAttribute", s.Attributes["role"])
	}
	validate := func(v string) bool {
		req := validator.StringRequest{Path: path.Root("role"), ConfigValue: types.StringValue(v)}
		for _, val := range role.Validators {
			var resp validator.StringResponse
			val.ValidateString(context.Background(), req, &resp)
			if resp.Diagnostics.HasError() {
				return false
			}
		}
		return true
	}
	for _, v := range allOrganizationRoles {
		if !validate(v) {
			t.Errorf("role %q rejected at plan time", v)
		}
	}
	if validate("") {
		t.Error("empty role accepted")
	}
	for _, want := range []string{"`admin`", "`owner`", "`membership_admin`", "`primary_owner`", "cannot be assigned", "`write:rbac_groups`"} {
		if !strings.Contains(role.MarkdownDescription, want) {
			t.Errorf("role description does not mention %s: %q", want, role.MarkdownDescription)
		}
	}
}

func TestInviteResource_roleAttribute(t *testing.T) {
	assertRoleAttribute(t, schematest.ResourceSchema(t, NewInviteResource()))
}

// TestInviteResource_importState verifies ImportState wires the id attribute.
func TestInviteResource_importState(t *testing.T) {
	r, ok := NewInviteResource().(resource.ResourceWithImportState)
	if !ok {
		t.Fatal("resource does not implement ResourceWithImportState")
	}

	state := schematest.NullState(t, r)

	req := resource.ImportStateRequest{ID: "invite_01ABC"}
	resp := &resource.ImportStateResponse{State: state}
	r.ImportState(context.Background(), req, resp)

	if resp.Diagnostics.HasError() {
		t.Fatalf("ImportState diagnostics: %v", resp.Diagnostics)
	}

	var got InviteResourceModel
	resp.Diagnostics.Append(resp.State.Get(context.Background(), &got)...)
	if resp.Diagnostics.HasError() {
		t.Fatalf("State.Get diagnostics: %v", resp.Diagnostics)
	}

	if got.ID.ValueString() != "invite_01ABC" {
		t.Errorf("ID = %q, want invite_01ABC", got.ID.ValueString())
	}
}
