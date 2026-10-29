// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package serviceaccounts

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ippontech/terraform-provider-anthropic/internal/oauthtest"
	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
)

// --- mapServiceAccountWorkspaceToState ---

func TestMapServiceAccountWorkspaceToState_BasicFields(t *testing.T) {
	member := &anthropic.BetaServiceAccountWorkspaceMember{
		ServiceAccountID: "svac_01ABC",
		WorkspaceID:      "wrkspc_01XYZ",
		WorkspaceRole:    anthropic.BetaWorkspaceRoleWorkspaceDeveloper,
		Implicit:         false,
		CreatedByActorID: "user_01CREATOR",
	}

	var data ServiceAccountWorkspaceResourceModel
	mapServiceAccountWorkspaceToState(member, &data)

	if got := data.ID.ValueString(); got != "svac_01ABC:wrkspc_01XYZ" {
		t.Errorf("ID = %q, want svac_01ABC:wrkspc_01XYZ", got)
	}
	if got := data.ServiceAccountID.ValueString(); got != "svac_01ABC" {
		t.Errorf("ServiceAccountID = %q, want svac_01ABC", got)
	}
	if got := data.WorkspaceID.ValueString(); got != "wrkspc_01XYZ" {
		t.Errorf("WorkspaceID = %q, want wrkspc_01XYZ", got)
	}
	if got := data.WorkspaceRole.ValueString(); got != "workspace_developer" {
		t.Errorf("WorkspaceRole = %q, want workspace_developer", got)
	}
	if data.Implicit.ValueBool() {
		t.Errorf("Implicit = true, want false")
	}
	if got := data.CreatedByActorID.ValueString(); got != "user_01CREATOR" {
		t.Errorf("CreatedByActorID = %q, want user_01CREATOR", got)
	}
}

func TestMapServiceAccountWorkspaceToState_Implicit(t *testing.T) {
	member := &anthropic.BetaServiceAccountWorkspaceMember{
		ServiceAccountID: "svac_01ABC",
		WorkspaceID:      "wrkspc_default",
		WorkspaceRole:    anthropic.BetaWorkspaceRoleWorkspaceUser,
		Implicit:         true,
	}

	var data ServiceAccountWorkspaceResourceModel
	mapServiceAccountWorkspaceToState(member, &data)

	if !data.Implicit.ValueBool() {
		t.Errorf("Implicit = false, want true")
	}
}

// --- ImportState: composite-ID split ---

func TestServiceAccountWorkspaceImportState_ValidCompositeID(t *testing.T) {
	ctx := context.Background()
	r := &ServiceAccountWorkspaceResource{}

	req := resource.ImportStateRequest{ID: "svac_01ABC:wrkspc_01XYZ"}
	resp := &resource.ImportStateResponse{State: schematest.NullState(t, &ServiceAccountWorkspaceResource{})}

	r.ImportState(ctx, req, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("unexpected diagnostics: %v", resp.Diagnostics)
	}

	var serviceAccountID, workspaceID, id types.String
	if d := resp.State.GetAttribute(ctx, path.Root("service_account_id"), &serviceAccountID); d.HasError() {
		t.Fatalf("GetAttribute(service_account_id): %v", d)
	}
	if d := resp.State.GetAttribute(ctx, path.Root("workspace_id"), &workspaceID); d.HasError() {
		t.Fatalf("GetAttribute(workspace_id): %v", d)
	}
	if d := resp.State.GetAttribute(ctx, path.Root("id"), &id); d.HasError() {
		t.Fatalf("GetAttribute(id): %v", d)
	}

	if got := serviceAccountID.ValueString(); got != "svac_01ABC" {
		t.Errorf("service_account_id = %q, want svac_01ABC", got)
	}
	if got := workspaceID.ValueString(); got != "wrkspc_01XYZ" {
		t.Errorf("workspace_id = %q, want wrkspc_01XYZ", got)
	}
	if got := id.ValueString(); got != "svac_01ABC:wrkspc_01XYZ" {
		t.Errorf("id = %q, want svac_01ABC:wrkspc_01XYZ", got)
	}
}

func TestServiceAccountWorkspaceImportState_InvalidFormat(t *testing.T) {
	cases := []string{
		"svac_01ABC",                    // missing separator
		"svac_01ABC:",                   // empty workspace_id
		":wrkspc_01XYZ",                 // empty service_account_id
		"",                              // empty
		"svac_01ABC:wrkspc_01XYZ:extra", // still valid: SplitN(2) keeps the extra segment in workspace_id
	}

	for _, id := range cases {
		t.Run(id, func(t *testing.T) {
			ctx := context.Background()
			r := &ServiceAccountWorkspaceResource{}
			req := resource.ImportStateRequest{ID: id}
			resp := &resource.ImportStateResponse{State: schematest.NullState(t, &ServiceAccountWorkspaceResource{})}

			r.ImportState(ctx, req, resp)

			wantErr := id != "svac_01ABC:wrkspc_01XYZ:extra"
			if wantErr && !resp.Diagnostics.HasError() {
				t.Errorf("expected an error for import ID %q", id)
			}
			if !wantErr && resp.Diagnostics.HasError() {
				t.Errorf("unexpected diagnostics for import ID %q: %v", id, resp.Diagnostics)
			}
		})
	}
}

// --- addServiceAccountToWorkspace: Add param construction ---

func TestAddServiceAccountToWorkspace_SendsCorrectPathAndBody(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{
			"created_by_actor_id": "user_01CREATOR",
			"implicit": false,
			"service_account_id": "svac_01ABC",
			"type": "service_account_workspace_member",
			"workspace_id": "wrkspc_01XYZ",
			"workspace_role": "workspace_developer"
		}`)
	}))
	defer srv.Close()

	client := oauthtest.NewClient(t, srv)
	member, err := addServiceAccountToWorkspace(context.Background(), client, "svac_01ABC", "wrkspc_01XYZ", "workspace_developer")
	if err != nil {
		t.Fatalf("addServiceAccountToWorkspace: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	if gotPath != "/v1/organizations/service_accounts/svac_01ABC/workspaces" {
		t.Errorf("path = %q, want /v1/organizations/service_accounts/svac_01ABC/workspaces", gotPath)
	}
	if !strings.Contains(gotBody, `"workspace_id":"wrkspc_01XYZ"`) || !strings.Contains(gotBody, `"workspace_role":"workspace_developer"`) {
		t.Errorf("body = %q, want it to contain workspace_id and workspace_role", gotBody)
	}
	if member.WorkspaceID != "wrkspc_01XYZ" {
		t.Errorf("member.WorkspaceID = %q, want wrkspc_01XYZ", member.WorkspaceID)
	}
}

// --- removeServiceAccountFromWorkspace: inverted-arg wiring ---

// TestRemoveServiceAccountFromWorkspace_RequestPathArgumentOrder locks in the
// SDK's inverted Remove signature: the workspace ID is the positional
// argument and the service account ID travels inside the params struct. If
// removeServiceAccountFromWorkspace ever passed these in the wrong order
// (e.g. swapping which local variable feeds the positional argument vs.
// params.ServiceAccountID), the resulting URL would still look plausible —
// both segments are tagged IDs — but would silently target the wrong
// resource. This test uses two distinct, easily distinguishable IDs and
// asserts their exact position in the URL.
func TestRemoveServiceAccountFromWorkspace_RequestPathArgumentOrder(t *testing.T) {
	var gotMethod, gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{
			"service_account_id": "svac_THESERVICEACCOUNT",
			"type": "service_account_workspace_member_deleted",
			"workspace_id": "wrkspc_THEWORKSPACE"
		}`)
	}))
	defer srv.Close()

	client := oauthtest.NewClient(t, srv)
	err := removeServiceAccountFromWorkspace(context.Background(), client, "svac_THESERVICEACCOUNT", "wrkspc_THEWORKSPACE")
	if err != nil {
		t.Fatalf("removeServiceAccountFromWorkspace: %v", err)
	}

	if gotMethod != http.MethodDelete {
		t.Errorf("method = %q, want DELETE", gotMethod)
	}
	want := "/v1/organizations/service_accounts/svac_THESERVICEACCOUNT/workspaces/wrkspc_THEWORKSPACE"
	if gotPath != want {
		t.Errorf("path = %q, want %q (service_account_id must come before workspace_id in the URL)", gotPath, want)
	}
}

// --- updateServiceAccountWorkspaceRole: inverted-arg wiring, the other way round ---

// TestUpdateServiceAccountWorkspaceRole_RequestPathArgumentOrder locks in the
// workspace-side Update signature, which inverts Remove's: the service account
// ID is the positional argument and the workspace ID travels inside the params
// struct, while the URL puts the workspace first. A swap would still produce a
// plausible URL (two tagged IDs), so the test asserts each ID's exact position
// and the exact body.
func TestUpdateServiceAccountWorkspaceRole_RequestPathArgumentOrder(t *testing.T) {
	var gotMethod, gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, serviceAccountWorkspaceMemberJSON("svac_THESERVICEACCOUNT", "wrkspc_THEWORKSPACE", "workspace_admin"))
	}))
	defer srv.Close()

	client := oauthtest.NewClient(t, srv)
	member, err := updateServiceAccountWorkspaceRole(context.Background(), client, "svac_THESERVICEACCOUNT", "wrkspc_THEWORKSPACE", "workspace_admin")
	if err != nil {
		t.Fatalf("updateServiceAccountWorkspaceRole: %v", err)
	}

	if gotMethod != http.MethodPost {
		t.Errorf("method = %q, want POST", gotMethod)
	}
	want := "/v1/organizations/workspaces/wrkspc_THEWORKSPACE/service_accounts/svac_THESERVICEACCOUNT"
	if gotPath != want {
		t.Errorf("path = %q, want %q (workspace_id must come before service_account_id in the URL)", gotPath, want)
	}
	if gotBody != `{"workspace_role":"workspace_admin"}` {
		t.Errorf("body = %s, want {\"workspace_role\":\"workspace_admin\"}", gotBody)
	}
	if member.WorkspaceRole != anthropic.BetaWorkspaceRoleWorkspaceAdmin {
		t.Errorf("member.WorkspaceRole = %q, want workspace_admin", member.WorkspaceRole)
	}
}

// serviceAccountWorkspaceMemberJSON is an explicit membership as the API
// returns it.
func serviceAccountWorkspaceMemberJSON(serviceAccountID, workspaceID, role string) string {
	return `{"created_by_actor_id": "user_01CREATOR", "implicit": false, "service_account_id": "` + serviceAccountID +
		`", "type": "service_account_workspace_member", "workspace_id": "` + workspaceID + `", "workspace_role": "` + role + `"}`
}

// --- Schema: only the two IDs force replacement ---

func TestServiceAccountWorkspaceSchema_OnlyIDsRequireReplace(t *testing.T) {
	ctx := context.Background()
	requiresReplace := stringplanmodifier.RequiresReplace().Description(ctx)
	attrs := schematest.ResourceSchema(t, &ServiceAccountWorkspaceResource{}).Attributes

	for name, want := range map[string]bool{"service_account_id": true, "workspace_id": true, "workspace_role": false} {
		attr, ok := attrs[name].(rschema.StringAttribute)
		if !ok {
			t.Fatalf("%s is not a StringAttribute", name)
		}
		got := false
		for _, m := range attr.PlanModifiers {
			if m.Description(ctx) == requiresReplace {
				got = true
			}
		}
		if got != want {
			t.Errorf("%s RequiresReplace = %v, want %v", name, got, want)
		}
	}
}

// --- Update: in place, no remove/re-add ---

// serviceAccountWorkspaceUpdateRequest builds the Update request for a membership
// whose role changes from oldRole to newRole.
func serviceAccountWorkspaceUpdateRequest(t *testing.T, r *ServiceAccountWorkspaceResource, oldRole, newRole string) (resource.UpdateRequest, *resource.UpdateResponse) {
	t.Helper()

	s := schematest.ResourceSchema(t, r)
	state := ServiceAccountWorkspaceResourceModel{
		ID:               types.StringValue("svac_01ABC:wrkspc_01XYZ"),
		ServiceAccountID: types.StringValue("svac_01ABC"),
		WorkspaceID:      types.StringValue("wrkspc_01XYZ"),
		WorkspaceRole:    types.StringValue(oldRole),
		Implicit:         types.BoolValue(false),
		CreatedByActorID: types.StringValue("user_01CREATOR"),
	}
	plan := state
	plan.WorkspaceRole = types.StringValue(newRole)

	planTF := tfsdk.Plan{Schema: s}
	if diags := planTF.Set(context.Background(), &plan); diags.HasError() {
		t.Fatalf("set plan: %v", diags)
	}
	stateTF := tfsdk.State{Schema: s}
	if diags := stateTF.Set(context.Background(), &state); diags.HasError() {
		t.Fatalf("set state: %v", diags)
	}
	return resource.UpdateRequest{Plan: planTF, State: stateTF, Config: tfsdk.Config{Schema: s, Raw: planTF.Raw}},
		&resource.UpdateResponse{State: tfsdk.State{Schema: s}}
}

func TestServiceAccountWorkspaceUpdate_ChangesRoleInPlace(t *testing.T) {
	var requests []string
	var updateBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/organizations/workspaces/wrkspc_01XYZ/service_accounts/svac_01ABC":
			b, _ := io.ReadAll(r.Body)
			updateBody = string(b)
			_, _ = io.WriteString(w, serviceAccountWorkspaceMemberJSON("svac_01ABC", "wrkspc_01XYZ", "workspace_admin"))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/organizations/service_accounts/svac_01ABC/workspaces":
			// The consistency wait: the list already shows the new role.
			_, _ = io.WriteString(w, `{"data": [`+serviceAccountWorkspaceMemberJSON("svac_01ABC", "wrkspc_01XYZ", "workspace_admin")+`], "next_page": null}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	r := &ServiceAccountWorkspaceResource{client: oauthtest.NewClient(t, srv)}
	req, resp := serviceAccountWorkspaceUpdateRequest(t, r, "workspace_developer", "workspace_admin")

	r.Update(context.Background(), req, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Update returned errors: %v", resp.Diagnostics)
	}

	// One update, then the consistency wait's list: never a remove and re-add.
	want := []string{
		"POST /v1/organizations/workspaces/wrkspc_01XYZ/service_accounts/svac_01ABC",
		"GET /v1/organizations/service_accounts/svac_01ABC/workspaces",
	}
	if strings.Join(requests, "\n") != strings.Join(want, "\n") {
		t.Errorf("requests = %v, want %v", requests, want)
	}
	if updateBody != `{"workspace_role":"workspace_admin"}` {
		t.Errorf("update body = %s", updateBody)
	}

	var got ServiceAccountWorkspaceResourceModel
	if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("get state: %v", diags)
	}
	if got.WorkspaceRole.ValueString() != "workspace_admin" {
		t.Errorf("workspace_role = %q, want workspace_admin", got.WorkspaceRole.ValueString())
	}
	if got.ID.ValueString() != "svac_01ABC:wrkspc_01XYZ" || got.CreatedByActorID.ValueString() != "user_01CREATOR" || got.Implicit.ValueBool() {
		t.Errorf("unexpected state after update: %+v", got)
	}
}

// An archived workspace answers the update with a 400: the error must name
// both IDs and keep the API message, and the prior state must be kept.
func TestServiceAccountWorkspaceUpdate_ErrorKeepsAPIMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"invalid_request_error","message":"Workspace is archived"}}`)
	}))
	defer srv.Close()

	r := &ServiceAccountWorkspaceResource{client: oauthtest.NewClient(t, srv)}
	req, resp := serviceAccountWorkspaceUpdateRequest(t, r, "workspace_developer", "workspace_admin")

	r.Update(context.Background(), req, resp)
	if !resp.Diagnostics.HasError() {
		t.Fatal("expected an error diagnostic")
	}
	detail := resp.Diagnostics.Errors()[0].Detail()
	for _, want := range []string{"svac_01ABC", "wrkspc_01XYZ", "Workspace is archived"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail does not mention %q: %s", want, detail)
		}
	}
	if !resp.State.Raw.IsNull() {
		t.Error("a failed update must not write state")
	}
}

// A role change on a membership removed since the plan (-refresh=false, or a
// removal between plan and apply) gets a 404 from the update endpoint: the
// membership is added back with the planned role, as the replace this
// attribute used to force did, instead of failing the apply.
func TestServiceAccountWorkspaceUpdate_MembershipGoneIsAddedBack(t *testing.T) {
	var requests []string
	var addBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/v1/organizations/workspaces/wrkspc_01XYZ/service_accounts/svac_01ABC":
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"type":"error","error":{"type":"not_found_error","message":"membership not found"}}`)
		case r.Method == http.MethodPost && r.URL.Path == "/v1/organizations/service_accounts/svac_01ABC/workspaces":
			b, _ := io.ReadAll(r.Body)
			addBody = string(b)
			_, _ = io.WriteString(w, serviceAccountWorkspaceMemberJSON("svac_01ABC", "wrkspc_01XYZ", "workspace_admin"))
		case r.Method == http.MethodGet && r.URL.Path == "/v1/organizations/service_accounts/svac_01ABC/workspaces":
			_, _ = io.WriteString(w, `{"data": [`+serviceAccountWorkspaceMemberJSON("svac_01ABC", "wrkspc_01XYZ", "workspace_admin")+`], "next_page": null}`)
		default:
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	r := &ServiceAccountWorkspaceResource{client: oauthtest.NewClient(t, srv)}
	req, resp := serviceAccountWorkspaceUpdateRequest(t, r, "workspace_developer", "workspace_admin")

	r.Update(context.Background(), req, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Update returned errors: %v", resp.Diagnostics)
	}
	want := []string{
		"POST /v1/organizations/workspaces/wrkspc_01XYZ/service_accounts/svac_01ABC",
		"POST /v1/organizations/service_accounts/svac_01ABC/workspaces",
		"GET /v1/organizations/service_accounts/svac_01ABC/workspaces",
	}
	if strings.Join(requests, "\n") != strings.Join(want, "\n") {
		t.Errorf("requests = %v, want %v", requests, want)
	}
	if !strings.Contains(addBody, `"workspace_role":"workspace_admin"`) || !strings.Contains(addBody, `"workspace_id":"wrkspc_01XYZ"`) {
		t.Errorf("add body = %s, want the workspace and the planned role", addBody)
	}

	var got ServiceAccountWorkspaceResourceModel
	if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("get state: %v", diags)
	}
	if got.WorkspaceRole.ValueString() != "workspace_admin" {
		t.Errorf("workspace_role = %q, want workspace_admin", got.WorkspaceRole.ValueString())
	}
}

// The update response is not mapped over the plan: implicit and
// created_by_actor_id are pinned from state, so a response that leaves out
// or changes created_by_actor_id must not turn a successful role change into
// an inconsistent result.
func TestServiceAccountWorkspaceUpdate_KeepsPlannedComputedValues(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			_, _ = io.WriteString(w, `{"implicit": false, "service_account_id": "svac_01ABC", "type": "service_account_workspace_member", "workspace_id": "wrkspc_01XYZ", "workspace_role": "workspace_admin"}`)
			return
		}
		_, _ = io.WriteString(w, `{"data": [`+serviceAccountWorkspaceMemberJSON("svac_01ABC", "wrkspc_01XYZ", "workspace_admin")+`], "next_page": null}`)
	}))
	defer srv.Close()

	r := &ServiceAccountWorkspaceResource{client: oauthtest.NewClient(t, srv)}
	req, resp := serviceAccountWorkspaceUpdateRequest(t, r, "workspace_developer", "workspace_admin")

	r.Update(context.Background(), req, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Update returned errors: %v", resp.Diagnostics)
	}
	var got ServiceAccountWorkspaceResourceModel
	if diags := resp.State.Get(context.Background(), &got); diags.HasError() {
		t.Fatalf("get state: %v", diags)
	}
	if got.CreatedByActorID.ValueString() != "user_01CREATOR" || got.ID.ValueString() != "svac_01ABC:wrkspc_01XYZ" || got.Implicit.ValueBool() {
		t.Errorf("state = %+v, want the planned id, implicit and created_by_actor_id", got)
	}
}

// --- findServiceAccountWorkspaceMembership: find-in-list, pagination, gone-from-list, implicit skip ---

func TestFindServiceAccountWorkspaceMembership_FoundOnFirstPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{
			"data": [
				{"created_by_actor_id": "user_01A", "implicit": false, "service_account_id": "svac_01ABC", "type": "service_account_workspace_member", "workspace_id": "wrkspc_01TARGET", "workspace_role": "workspace_admin"}
			],
			"next_page": null
		}`)
	}))
	defer srv.Close()

	client := oauthtest.NewClient(t, srv)
	member, err := findServiceAccountWorkspaceMembership(context.Background(), client, "svac_01ABC", "wrkspc_01TARGET")
	if err != nil {
		t.Fatalf("findServiceAccountWorkspaceMembership: %v", err)
	}
	if member == nil {
		t.Fatal("expected a membership, got nil")
	}
	if member.WorkspaceID != "wrkspc_01TARGET" {
		t.Errorf("WorkspaceID = %q, want wrkspc_01TARGET", member.WorkspaceID)
	}
}

func TestFindServiceAccountWorkspaceMembership_FoundOnSecondPage(t *testing.T) {
	requestCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestCount++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if r.URL.Query().Get("page") == "" {
			_, _ = io.WriteString(w, `{
				"data": [
					{"created_by_actor_id": "user_01A", "implicit": false, "service_account_id": "svac_01ABC", "type": "service_account_workspace_member", "workspace_id": "wrkspc_OTHER", "workspace_role": "workspace_user"}
				],
				"next_page": "cursor-2"
			}`)
			return
		}
		_, _ = io.WriteString(w, `{
			"data": [
				{"created_by_actor_id": "user_01A", "implicit": false, "service_account_id": "svac_01ABC", "type": "service_account_workspace_member", "workspace_id": "wrkspc_01TARGET", "workspace_role": "workspace_admin"}
			],
			"next_page": null
		}`)
	}))
	defer srv.Close()

	client := oauthtest.NewClient(t, srv)
	member, err := findServiceAccountWorkspaceMembership(context.Background(), client, "svac_01ABC", "wrkspc_01TARGET")
	if err != nil {
		t.Fatalf("findServiceAccountWorkspaceMembership: %v", err)
	}
	if member == nil {
		t.Fatal("expected a membership, got nil")
	}
	if requestCount < 2 {
		t.Errorf("expected pagination to fetch a second page, got %d requests", requestCount)
	}
}

func TestFindServiceAccountWorkspaceMembership_GoneFromList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"data": [], "next_page": null}`)
	}))
	defer srv.Close()

	client := oauthtest.NewClient(t, srv)
	member, err := findServiceAccountWorkspaceMembership(context.Background(), client, "svac_01ABC", "wrkspc_01TARGET")
	if err != nil {
		t.Fatalf("findServiceAccountWorkspaceMembership: %v", err)
	}
	if member != nil {
		t.Errorf("expected nil membership, got %+v", member)
	}
}

func TestFindServiceAccountWorkspaceMembership_SkipsImplicitEntry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		// The target workspace_id matches, but the entry is the implicit
		// default-workspace membership: the explicit one this resource
		// manages was removed out-of-band, so this must be treated as gone.
		_, _ = io.WriteString(w, `{
			"data": [
				{"created_by_actor_id": "user_01A", "implicit": true, "service_account_id": "svac_01ABC", "type": "service_account_workspace_member", "workspace_id": "wrkspc_01TARGET", "workspace_role": "workspace_user"}
			],
			"next_page": null
		}`)
	}))
	defer srv.Close()

	client := oauthtest.NewClient(t, srv)
	member, err := findServiceAccountWorkspaceMembership(context.Background(), client, "svac_01ABC", "wrkspc_01TARGET")
	if err != nil {
		t.Fatalf("findServiceAccountWorkspaceMembership: %v", err)
	}
	if member != nil {
		t.Errorf("expected nil (implicit entry must not count as a match), got %+v", member)
	}
}

func TestFindServiceAccountWorkspaceMembership_NotFoundError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"not_found_error","message":"service account not found"}}`)
	}))
	defer srv.Close()

	client := oauthtest.NewClient(t, srv)
	_, err := findServiceAccountWorkspaceMembership(context.Background(), client, "svac_missing", "wrkspc_01TARGET")

	var apierr *anthropic.Error
	if !errors.As(err, &apierr) {
		t.Fatalf("expected *anthropic.Error, got: %v", err)
	}
	if apierr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want 404", apierr.StatusCode)
	}
}
