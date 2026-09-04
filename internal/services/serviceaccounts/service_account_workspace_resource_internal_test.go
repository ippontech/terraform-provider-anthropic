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
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// newTestOAuthClient builds an OAuth-wrapped SDK client pointed at an
// httptest server, matching the pattern the OAuth-gated resources in this
// provider use for CI-deterministic unit tests (see
// internal/services/vaults/vault_resource_internal_test.go and the sibling
// federation_rule_resource_internal_test.go).
func newTestOAuthClient(t *testing.T, srv *httptest.Server) *providerdata.OAuthClient {
	t.Helper()
	c := anthropic.NewClient(option.WithBaseURL(srv.URL), option.WithAuthToken("test"))
	return &providerdata.OAuthClient{Client: &c}
}

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

func schemaType(t *testing.T) tftypes.Type {
	t.Helper()
	var schemaResp resource.SchemaResponse
	(&ServiceAccountWorkspaceResource{}).Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)
	tfType, ok := schemaResp.Schema.Type().(interface {
		TerraformType(context.Context) tftypes.Type
	})
	if !ok {
		t.Fatal("schema type does not implement TerraformType")
	}
	return tfType.TerraformType(context.Background())
}

func nullValuesForSchema(t *testing.T) map[string]tftypes.Value {
	t.Helper()
	schemaObjType := schemaType(t).(tftypes.Object)
	vals := make(map[string]tftypes.Value, len(schemaObjType.AttributeTypes))
	for name, typ := range schemaObjType.AttributeTypes {
		vals[name] = tftypes.NewValue(typ, nil)
	}
	return vals
}

func newNullState(t *testing.T) tfsdk.State {
	t.Helper()
	var schemaResp resource.SchemaResponse
	(&ServiceAccountWorkspaceResource{}).Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)
	rawVal := tftypes.NewValue(schemaType(t), nullValuesForSchema(t))
	return tfsdk.State{Raw: rawVal, Schema: schemaResp.Schema}
}

func TestServiceAccountWorkspaceImportState_ValidCompositeID(t *testing.T) {
	ctx := context.Background()
	r := &ServiceAccountWorkspaceResource{}

	req := resource.ImportStateRequest{ID: "svac_01ABC:wrkspc_01XYZ"}
	resp := &resource.ImportStateResponse{State: newNullState(t)}

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
			resp := &resource.ImportStateResponse{State: newNullState(t)}

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

	client := newTestOAuthClient(t, srv)
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

	client := newTestOAuthClient(t, srv)
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

	client := newTestOAuthClient(t, srv)
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

	client := newTestOAuthClient(t, srv)
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

	client := newTestOAuthClient(t, srv)
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

	client := newTestOAuthClient(t, srv)
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

	client := newTestOAuthClient(t, srv)
	_, err := findServiceAccountWorkspaceMembership(context.Background(), client, "svac_missing", "wrkspc_01TARGET")

	var apierr *anthropic.Error
	if !errors.As(err, &apierr) {
		t.Fatalf("expected *anthropic.Error, got: %v", err)
	}
	if apierr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want 404", apierr.StatusCode)
	}
}
