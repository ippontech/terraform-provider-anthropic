// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package organizations

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
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
	if diags := mapInviteToState(&inv, &data); diags.HasError() {
		t.Fatalf("map: %v", diags)
	}

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

// --- rbac_group_ids ---

const (
	rbacGroupA = "rbac_group_01UvWxYzAbCdEfGhIjKlMn"
	rbacGroupB = "rbac_group_01CdEfGhIjKlMnOpQrStUv"
)

// inviteFixtureWithGroups is a pending managed invite whose rbac_group_ids
// field is rbacGroupIDs verbatim (a JSON array or `null`), or absent when
// rbacGroupIDs is empty.
func inviteFixtureWithGroups(rbacGroupIDs string) string {
	field := ""
	if rbacGroupIDs != "" {
		field = `"rbac_group_ids": ` + rbacGroupIDs + `,`
	}
	return `{
		"id": "invite_01ABC",
		"email": "new-engineer@example.com",
		"role": "managed",
		` + field + `
		"status": "pending",
		"invited_at": "2026-07-06T16:20:11Z",
		"expires_at": "2026-07-27T16:20:11Z",
		"accepted_at": null,
		"type": "invite"
	}`
}

// rbacGroupIDsOf returns the sorted elements of a known set, or nil for a
// null set.
func rbacGroupIDsOf(t *testing.T, v types.Set) []string {
	t.Helper()
	if v.IsNull() {
		return nil
	}
	ids := []string{}
	if diags := v.ElementsAs(context.Background(), &ids, false); diags.HasError() {
		t.Fatalf("rbac_group_ids: %v", diags)
	}
	sort.Strings(ids)
	return ids
}

func inviteState(t *testing.T, s tfsdk.State) InviteResourceModel {
	t.Helper()
	var m InviteResourceModel
	if diags := s.Get(context.Background(), &m); diags.HasError() {
		t.Fatalf("state: %v", diags)
	}
	return m
}

// invitePlan is the plan of a new managed invite with the given
// rbac_group_ids override (nil leaves the attribute unset).
func invitePlan(t *testing.T, r resource.Resource, rbacGroupIDs any) tfsdk.Plan {
	t.Helper()
	return Plan(t, r, map[string]any{
		"email":          "new-engineer@example.com",
		"role":           "managed",
		"rbac_group_ids": rbacGroupIDs,
		"id":             Unknown,
		"status":         Unknown,
		"invited_at":     Unknown,
		"expires_at":     Unknown,
		"accepted_at":    Unknown,
		"type":           Unknown,
	})
}

func TestInviteCreate_sendsRBACGroupIDs(t *testing.T) {
	t.Parallel()

	srv, rec := stub(t, func(recordedRequest) (int, string) {
		return http.StatusOK, inviteFixtureWithGroups(`["` + rbacGroupA + `", "` + rbacGroupB + `"]`)
	})
	r := &InviteResource{adminClient: admintest.NewClient(t, srv)}

	resp := resource.CreateResponse{State: tfsdk.State{Schema: schematest.ResourceSchema(t, r)}}
	r.Create(context.Background(), resource.CreateRequest{Plan: invitePlan(t, r, []string{rbacGroupA, rbacGroupB})}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create: %v", resp.Diagnostics)
	}

	reqs := rec.all()
	if len(reqs) != 1 {
		t.Fatalf("requests = %d, want 1", len(reqs))
	}
	req := reqs[0]
	if req.Method != http.MethodPost || req.Path != "/v1/organizations/invites" {
		t.Errorf("request = %s %s, want POST /v1/organizations/invites", req.Method, req.Path)
	}
	assertBetaQuery(t, req)
	if req.Body["email"] != "new-engineer@example.com" || req.Body["role"] != "managed" {
		t.Errorf("body = %v, want the planned email and role", req.Body)
	}
	got, ok := req.Body["rbac_group_ids"].([]any)
	if !ok || len(got) != 2 || got[0] != rbacGroupB || got[1] != rbacGroupA {
		t.Errorf("body rbac_group_ids = %#v, want [%s %s] (sorted)", req.Body["rbac_group_ids"], rbacGroupB, rbacGroupA)
	}

	state := inviteState(t, resp.State)
	if ids := rbacGroupIDsOf(t, state.RBACGroupIDs); len(ids) != 2 || ids[0] != rbacGroupB || ids[1] != rbacGroupA {
		t.Errorf("state rbac_group_ids = %v, want both groups", ids)
	}
	if state.ID.ValueString() != "invite_01ABC" || state.Status.ValueString() != "pending" {
		t.Errorf("id/status = %v/%v", state.ID, state.Status)
	}
}

// TestInviteCreate_omitsNoRBACGroups checks that an unset or empty
// rbac_group_ids sends no field, so an invite without groups needs neither a
// Claude Enterprise organization nor the write:rbac_groups scope, and that
// the API's `[]` keeps the planned form (null or empty) in state, as
// Terraform requires after apply.
func TestInviteCreate_omitsNoRBACGroups(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name      string
		planned   any // nil: unset
		wantEmpty bool
	}{
		{name: "unset"},
		{name: "empty", planned: []string{}, wantEmpty: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			srv, rec := stub(t, func(recordedRequest) (int, string) {
				return http.StatusOK, inviteFixtureWithGroups(`[]`)
			})
			r := &InviteResource{adminClient: admintest.NewClient(t, srv)}

			resp := resource.CreateResponse{State: tfsdk.State{Schema: schematest.ResourceSchema(t, r)}}
			r.Create(context.Background(), resource.CreateRequest{Plan: invitePlan(t, r, test.planned)}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("Create: %v", resp.Diagnostics)
			}

			body := rec.all()[0].Body
			if _, ok := body["rbac_group_ids"]; ok {
				t.Errorf("body = %v, want no rbac_group_ids", body)
			}
			if len(body) != 2 {
				t.Errorf("body = %v, want only email and role", body)
			}

			got := inviteState(t, resp.State).RBACGroupIDs
			if got.IsNull() == test.wantEmpty || len(got.Elements()) != 0 {
				t.Errorf("state rbac_group_ids = %v, want the planned form (empty: %v)", got, test.wantEmpty)
			}
		})
	}
}

func TestInviteRead_mapsRBACGroupIDs(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name     string
		prior    any      // nil: unset
		response string   // rbac_group_ids in the response; "" leaves it out
		want     []string // nil: null
	}{
		{name: "groups added out of band", response: `["` + rbacGroupA + `"]`, want: []string{rbacGroupA}},
		{name: "no group, unset", response: `[]`},
		{name: "no group, empty", prior: []string{}, response: `[]`, want: []string{}},
		{name: "groups gone", prior: []string{rbacGroupA}, response: `[]`, want: []string{}},
		{name: "groups read back", prior: []string{rbacGroupA}, response: `["` + rbacGroupA + `", "` + rbacGroupB + `"]`, want: []string{rbacGroupB, rbacGroupA}},
		{name: "field absent keeps state", prior: []string{rbacGroupA}, want: []string{rbacGroupA}},
		{name: "field null keeps state", prior: []string{rbacGroupA}, response: `null`, want: []string{rbacGroupA}},
		{name: "field absent, unset", response: ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			srv, rec := stub(t, func(recordedRequest) (int, string) {
				return http.StatusOK, inviteFixtureWithGroups(test.response)
			})
			r := &InviteResource{adminClient: admintest.NewClient(t, srv)}

			state := State(t, r, map[string]any{
				"id": "invite_01ABC", "email": "new-engineer@example.com", "role": "managed", "status": "pending",
				"rbac_group_ids": test.prior,
			})
			resp := resource.ReadResponse{State: state}
			r.Read(context.Background(), resource.ReadRequest{State: state}, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("Read: %v", resp.Diagnostics)
			}

			req := rec.all()[0]
			if req.Method != http.MethodGet || req.Path != "/v1/organizations/invites/invite_01ABC" {
				t.Errorf("request = %s %s", req.Method, req.Path)
			}
			assertBetaQuery(t, req)

			got := rbacGroupIDsOf(t, inviteState(t, resp.State).RBACGroupIDs)
			if (got == nil) != (test.want == nil) || strings.Join(got, ",") != strings.Join(test.want, ",") {
				t.Errorf("rbac_group_ids = %#v, want %#v", got, test.want)
			}
		})
	}
}

// TestInviteResource_rbacGroupIDsRequiresReplace runs the schema's own plan
// modifiers: the API cannot update an invite, so any change to its groups
// replaces it, except switching between unset and an empty set.
func TestInviteResource_rbacGroupIDsRequiresReplace(t *testing.T) {
	t.Parallel()

	r := NewInviteResource()
	setAttr, ok := schematest.ResourceSchema(t, r).Attributes["rbac_group_ids"].(schema.SetAttribute)
	if !ok {
		t.Fatal("rbac_group_ids is not a SetAttribute")
	}

	for _, test := range []struct {
		name        string
		state, plan any // nil: unset
		wantReplace bool
	}{
		{name: "groups added", plan: []string{rbacGroupA}, wantReplace: true},
		{name: "groups removed", state: []string{rbacGroupA}, wantReplace: true},
		{name: "groups emptied", state: []string{rbacGroupA}, plan: []string{}, wantReplace: true},
		{name: "group swapped", state: []string{rbacGroupA}, plan: []string{rbacGroupB}, wantReplace: true},
		{name: "group added to existing", state: []string{rbacGroupA}, plan: []string{rbacGroupA, rbacGroupB}, wantReplace: true},
		{name: "unknown at plan", plan: Unknown, wantReplace: true},
		{name: "unchanged", state: []string{rbacGroupB, rbacGroupA}, plan: []string{rbacGroupA, rbacGroupB}},
		{name: "unset to empty", plan: []string{}},
		{name: "empty to unset", state: []string{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			invite := func(rbacGroupIDs any) map[string]any {
				return map[string]any{"id": "invite_01ABC", "email": "new-engineer@example.com", "role": "managed", "rbac_group_ids": rbacGroupIDs}
			}
			state := State(t, r, invite(test.state))
			plan := Plan(t, r, invite(test.plan))

			var stateValue, planValue types.Set
			state.GetAttribute(ctx, path.Root("rbac_group_ids"), &stateValue)
			plan.GetAttribute(ctx, path.Root("rbac_group_ids"), &planValue)

			req := planmodifier.SetRequest{
				Path:        path.Root("rbac_group_ids"),
				State:       state,
				Plan:        plan,
				StateValue:  stateValue,
				PlanValue:   planValue,
				ConfigValue: planValue,
			}
			resp := &planmodifier.SetResponse{PlanValue: planValue}
			for _, m := range setAttr.PlanModifiers {
				m.PlanModifySet(ctx, req, resp)
			}
			if resp.Diagnostics.HasError() {
				t.Fatalf("plan modifiers: %v", resp.Diagnostics)
			}
			if resp.RequiresReplace != test.wantReplace {
				t.Errorf("RequiresReplace = %v, want %v", resp.RequiresReplace, test.wantReplace)
			}
		})
	}
}

// TestInviteUpdate_switchesEmptyForm covers the one change that reaches
// Update: rbac_group_ids going from unset to an empty set. Nothing is sent,
// and the prior state is kept with the planned value, so the computed
// attributes the plan marks unknown stay known.
func TestInviteUpdate_switchesEmptyForm(t *testing.T) {
	t.Parallel()

	srv, rec := stub(t, func(recordedRequest) (int, string) { return http.StatusOK, `{}` })
	r := &InviteResource{adminClient: admintest.NewClient(t, srv)}

	prior := map[string]any{
		"id": "invite_01ABC", "email": "new-engineer@example.com", "role": "managed",
		"status": "pending", "invited_at": "2026-07-06T16:20:11Z", "expires_at": "2026-07-27T16:20:11Z",
		"accepted_at": "", "type": "invite",
	}
	planned := map[string]any{"status": Unknown, "accepted_at": Unknown, "rbac_group_ids": []string{}}
	for k, v := range prior {
		if _, ok := planned[k]; !ok {
			planned[k] = v
		}
	}
	state := State(t, r, prior)

	resp := resource.UpdateResponse{State: state}
	r.Update(context.Background(), resource.UpdateRequest{State: state, Plan: Plan(t, r, planned)}, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Update: %v", resp.Diagnostics)
	}
	if n := len(rec.all()); n != 0 {
		t.Errorf("requests = %d, want none", n)
	}

	got := inviteState(t, resp.State)
	if got.RBACGroupIDs.IsNull() || len(got.RBACGroupIDs.Elements()) != 0 {
		t.Errorf("rbac_group_ids = %v, want the planned empty set", got.RBACGroupIDs)
	}
	if got.Status.ValueString() != "pending" || got.AcceptedAt.IsUnknown() {
		t.Errorf("status/accepted_at = %v/%v, want the prior values", got.Status, got.AcceptedAt)
	}
}

// TestInviteResource_rbacGroupIDsValidators runs the schema's own validators.
func TestInviteResource_rbacGroupIDsValidators(t *testing.T) {
	t.Parallel()

	setAttr, ok := schematest.ResourceSchema(t, NewInviteResource()).Attributes["rbac_group_ids"].(schema.SetAttribute)
	if !ok {
		t.Fatal("rbac_group_ids is not a SetAttribute")
	}

	tooMany := make([]attr.Value, maxInviteRBACGroups+1)
	for i := range tooMany {
		tooMany[i] = types.StringValue(fmt.Sprintf("rbac_group_%03d", i))
	}

	for _, test := range []struct {
		name      string
		value     types.Set
		wantError bool
	}{
		{name: "valid", value: types.SetValueMust(types.StringType, []attr.Value{types.StringValue(rbacGroupA), types.StringValue(rbacGroupB)})},
		{name: "empty", value: types.SetValueMust(types.StringType, []attr.Value{})},
		{name: "unknown element", value: types.SetValueMust(types.StringType, []attr.Value{types.StringUnknown()})},
		{name: "unknown set", value: types.SetUnknown(types.StringType)},
		{name: "another ID type", value: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("wrkspc_01AbCdEf")}), wantError: true},
		{name: "prefix only", value: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("rbac_group_")}), wantError: true},
		{name: "trailing newline", value: types.SetValueMust(types.StringType, []attr.Value{types.StringValue(rbacGroupA + "\n")}), wantError: true},
		{name: "group name", value: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("Engineering")}), wantError: true},
		{name: "over the API limit", value: types.SetValueMust(types.StringType, tooMany), wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			req := validator.SetRequest{Path: path.Root("rbac_group_ids"), ConfigValue: test.value}
			resp := &validator.SetResponse{}
			for _, v := range setAttr.Validators {
				v.ValidateSet(context.Background(), req, resp)
			}
			if resp.Diagnostics.HasError() != test.wantError {
				t.Errorf("diagnostics = %v, wantError = %v", resp.Diagnostics, test.wantError)
			}
		})
	}
}
