// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// schemaType returns the tftypes.Type of the FederationRuleWorkspaceResource
// schema.
func schemaType(t *testing.T) tftypes.Type {
	t.Helper()
	var schemaResp resource.SchemaResponse
	r := NewFederationRuleWorkspaceResource()
	r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)
	tfType, ok := schemaResp.Schema.Type().(interface {
		TerraformType(context.Context) tftypes.Type
	})
	if !ok {
		t.Fatal("schema type does not implement TerraformType")
	}
	return tfType.TerraformType(context.Background())
}

// nullValuesForSchema returns a map of tftypes.Value with null values for
// every attribute in the schema — used as a base to build test states.
func nullValuesForSchema(t *testing.T) map[string]tftypes.Value {
	t.Helper()
	schemaObjType := schemaType(t).(tftypes.Object)
	vals := make(map[string]tftypes.Value, len(schemaObjType.AttributeTypes))
	for name, typ := range schemaObjType.AttributeTypes {
		vals[name] = tftypes.NewValue(typ, nil)
	}
	return vals
}

func newTestOAuthClient(srv *httptest.Server) *providerdata.OAuthClient {
	c := anthropic.NewClient(option.WithBaseURL(srv.URL), option.WithAuthToken("test"))
	return &providerdata.OAuthClient{Client: &c}
}

// newListWorkspacesServer serves a single-page ListAutoPaging response
// containing entries, and records every request's method and path.
func newListWorkspacesServer(t *testing.T, entries []anthropic.BetaFederationRuleWorkspace) (*httptest.Server, *[]string) {
	t.Helper()
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"data":      entries,
			"next_page": nil,
		}); err != nil {
			t.Errorf("encoding response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &requests
}

// ---------------------------------------------------------------------------
// ImportState — composite ID parsing
// ---------------------------------------------------------------------------

func TestFederationRuleWorkspaceImportState_ValidID(t *testing.T) {
	ctx := context.Background()
	r := NewFederationRuleWorkspaceResource()

	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	schemaObjType := schemaType(t).(tftypes.Object)
	rawVal := tftypes.NewValue(schemaObjType, nullValuesForSchema(t))
	state := tfsdk.State{Raw: rawVal, Schema: schemaResp.Schema}

	req := resource.ImportStateRequest{ID: "fdrl_01ABC:wrkspc_01XYZ"}
	var resp resource.ImportStateResponse
	resp.State = state

	r.(*FederationRuleWorkspaceResource).ImportState(ctx, req, &resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("ImportState: %v", resp.Diagnostics)
	}

	var ruleID types.String
	resp.Diagnostics.Append(resp.State.GetAttribute(ctx, path.Root("federation_rule_id"), &ruleID)...)
	if ruleID.ValueString() != "fdrl_01ABC" {
		t.Errorf("federation_rule_id: got %q, want fdrl_01ABC", ruleID.ValueString())
	}

	var workspaceID types.String
	resp.Diagnostics.Append(resp.State.GetAttribute(ctx, path.Root("workspace_id"), &workspaceID)...)
	if workspaceID.ValueString() != "wrkspc_01XYZ" {
		t.Errorf("workspace_id: got %q, want wrkspc_01XYZ", workspaceID.ValueString())
	}

	var id types.String
	resp.Diagnostics.Append(resp.State.GetAttribute(ctx, path.Root("id"), &id)...)
	if id.ValueString() != "fdrl_01ABC:wrkspc_01XYZ" {
		t.Errorf("id: got %q, want fdrl_01ABC:wrkspc_01XYZ", id.ValueString())
	}
}

func TestFederationRuleWorkspaceImportState_InvalidID(t *testing.T) {
	ctx := context.Background()
	r := NewFederationRuleWorkspaceResource()

	var schemaResp resource.SchemaResponse
	r.Schema(ctx, resource.SchemaRequest{}, &schemaResp)

	schemaObjType := schemaType(t).(tftypes.Object)
	rawVal := tftypes.NewValue(schemaObjType, nullValuesForSchema(t))
	state := tfsdk.State{Raw: rawVal, Schema: schemaResp.Schema}

	for _, id := range []string{"no-colon-here", "", ":missing-rule-id", "missing-workspace-id:"} {
		req := resource.ImportStateRequest{ID: id}
		var resp resource.ImportStateResponse
		resp.State = state

		r.(*FederationRuleWorkspaceResource).ImportState(ctx, req, &resp)
		if !resp.Diagnostics.HasError() {
			t.Errorf("ImportState(%q): expected an error, got none", id)
		}
	}
}

// ---------------------------------------------------------------------------
// mapFederationRuleWorkspaceToState
// ---------------------------------------------------------------------------

func TestMapFederationRuleWorkspaceToState_BasicFields(t *testing.T) {
	createdAt := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	w := &anthropic.BetaFederationRuleWorkspace{
		FederationRuleID: "fdrl_01ABC",
		WorkspaceID:      "wrkspc_01XYZ",
		WorkspaceName:    "production",
		CreatedAt:        createdAt,
		CreatedByActorID: "user_01CREATOR",
	}

	var data FederationRuleWorkspaceResourceModel
	mapFederationRuleWorkspaceToState(w, &data)

	if got := data.ID.ValueString(); got != "fdrl_01ABC:wrkspc_01XYZ" {
		t.Errorf("ID = %q, want fdrl_01ABC:wrkspc_01XYZ", got)
	}
	if got := data.FederationRuleID.ValueString(); got != "fdrl_01ABC" {
		t.Errorf("FederationRuleID = %q, want fdrl_01ABC", got)
	}
	if got := data.WorkspaceID.ValueString(); got != "wrkspc_01XYZ" {
		t.Errorf("WorkspaceID = %q, want wrkspc_01XYZ", got)
	}
	if got := data.WorkspaceName.ValueString(); got != "production" {
		t.Errorf("WorkspaceName = %q, want production", got)
	}
	if got := data.CreatedAt.ValueString(); got != "2024-01-15T10:00:00Z" {
		t.Errorf("CreatedAt = %q, want 2024-01-15T10:00:00Z", got)
	}
	if got := data.CreatedByActorID.ValueString(); got != "user_01CREATOR" {
		t.Errorf("CreatedByActorID = %q, want user_01CREATOR", got)
	}
}

func TestMapFederationRuleWorkspaceToState_EmptyFieldsAreNull(t *testing.T) {
	w := &anthropic.BetaFederationRuleWorkspace{
		FederationRuleID: "fdrl_01ABC",
		WorkspaceID:      "wrkspc_01XYZ",
		// WorkspaceName and CreatedByActorID are their Go zero value ("").
		// The comment on BetaFederationRuleWorkspace documents workspace_name
		// as "null in the enable response", and created_by_actor_id as
		// present "if known" — both cases map to null rather than "".
	}

	var data FederationRuleWorkspaceResourceModel
	mapFederationRuleWorkspaceToState(w, &data)

	if !data.WorkspaceName.IsNull() {
		t.Errorf("WorkspaceName = %q, want null", data.WorkspaceName.ValueString())
	}
	if !data.CreatedByActorID.IsNull() {
		t.Errorf("CreatedByActorID = %q, want null", data.CreatedByActorID.ValueString())
	}
	if !data.CreatedAt.IsNull() {
		t.Errorf("CreatedAt = %q, want null (zero time)", data.CreatedAt.ValueString())
	}
}

// ---------------------------------------------------------------------------
// findFederationRuleWorkspace
// ---------------------------------------------------------------------------

func TestFindFederationRuleWorkspace_Found(t *testing.T) {
	srv, requests := newListWorkspacesServer(t, []anthropic.BetaFederationRuleWorkspace{
		{FederationRuleID: "fdrl_01ABC", WorkspaceID: "wrkspc_OTHER", WorkspaceName: "other"},
		{FederationRuleID: "fdrl_01ABC", WorkspaceID: "wrkspc_01XYZ", WorkspaceName: "production"},
	})
	client := newTestOAuthClient(srv)

	found, err := findFederationRuleWorkspace(context.Background(), client.Client, "fdrl_01ABC", "wrkspc_01XYZ")
	if err != nil {
		t.Fatalf("findFederationRuleWorkspace: %v", err)
	}
	if found == nil {
		t.Fatal("expected a match, got nil")
	}
	if found.WorkspaceName != "production" {
		t.Errorf("WorkspaceName = %q, want production", found.WorkspaceName)
	}

	if len(*requests) != 1 || (*requests)[0] != "GET /v1/organizations/federation_rules/fdrl_01ABC/workspaces" {
		t.Errorf("requests = %v, want a single GET to the rule's workspaces endpoint", *requests)
	}
}

func TestFindFederationRuleWorkspace_GoneFromList(t *testing.T) {
	srv, _ := newListWorkspacesServer(t, []anthropic.BetaFederationRuleWorkspace{
		{FederationRuleID: "fdrl_01ABC", WorkspaceID: "wrkspc_OTHER", WorkspaceName: "other"},
	})
	client := newTestOAuthClient(srv)

	found, err := findFederationRuleWorkspace(context.Background(), client.Client, "fdrl_01ABC", "wrkspc_01XYZ")
	if err != nil {
		t.Fatalf("findFederationRuleWorkspace: %v", err)
	}
	if found != nil {
		t.Fatalf("expected no match, got %+v", found)
	}
}

func TestFindFederationRuleWorkspace_RuleGone404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"not_found_error","message":"rule not found"}}`)
	}))
	t.Cleanup(srv.Close)
	client := newTestOAuthClient(srv)

	_, err := findFederationRuleWorkspace(context.Background(), client.Client, "fdrl_01GONE", "wrkspc_01XYZ")
	if err == nil {
		t.Fatal("expected an error for a 404 response, got nil")
	}
	var apiErr *anthropic.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *anthropic.Error, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want 404", apiErr.StatusCode)
	}
}

// ---------------------------------------------------------------------------
// Create — Add wiring
// ---------------------------------------------------------------------------

func TestFederationRuleWorkspaceCreate_AddWiring(t *testing.T) {
	var addRequests []map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodPost:
			if got, want := r.URL.Path, "/v1/organizations/federation_rules/fdrl_01ABC/workspaces"; got != want {
				t.Errorf("POST path = %q, want %q", got, want)
			}
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decoding request body: %v", err)
			}
			addRequests = append(addRequests, body)
			if err := json.NewEncoder(w).Encode(map[string]any{
				"federation_rule_id":  "fdrl_01ABC",
				"workspace_id":        "wrkspc_01XYZ",
				"workspace_name":      nil,
				"created_at":          "2024-01-15T10:00:00Z",
				"created_by_actor_id": "user_01CREATOR",
				"type":                "federation_rule_workspace",
			}); err != nil {
				t.Errorf("encoding response: %v", err)
			}
		case http.MethodGet:
			// Enrichment lookup: report the entry as not (yet) listed, so
			// Create falls back to the Add response above.
			if err := json.NewEncoder(w).Encode(map[string]any{"data": []any{}, "next_page": nil}); err != nil {
				t.Errorf("encoding response: %v", err)
			}
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	t.Cleanup(srv.Close)

	r := &FederationRuleWorkspaceResource{client: newTestOAuthClient(srv)}

	var schemaResp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)

	schemaObjType := schemaType(t).(tftypes.Object)
	vals := nullValuesForSchema(t)
	vals["federation_rule_id"] = tftypes.NewValue(tftypes.String, "fdrl_01ABC")
	vals["workspace_id"] = tftypes.NewValue(tftypes.String, "wrkspc_01XYZ")
	rawVal := tftypes.NewValue(schemaObjType, vals)

	req := resource.CreateRequest{Plan: tfsdk.Plan{Raw: rawVal, Schema: schemaResp.Schema}}
	resp := &resource.CreateResponse{State: tfsdk.State{Raw: rawVal, Schema: schemaResp.Schema}}

	r.Create(context.Background(), req, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Create: %v", resp.Diagnostics)
	}

	if len(addRequests) != 1 {
		t.Fatalf("expected exactly one Add request, got %d", len(addRequests))
	}
	if got := addRequests[0]["workspace_id"]; got != "wrkspc_01XYZ" {
		t.Errorf("Add request workspace_id = %v, want wrkspc_01XYZ", got)
	}

	var id types.String
	resp.Diagnostics.Append(resp.State.GetAttribute(context.Background(), path.Root("id"), &id)...)
	if id.ValueString() != "fdrl_01ABC:wrkspc_01XYZ" {
		t.Errorf("id = %q, want fdrl_01ABC:wrkspc_01XYZ", id.ValueString())
	}
}

// ---------------------------------------------------------------------------
// Delete — Remove's inverted signature
// ---------------------------------------------------------------------------

// TestFederationRuleWorkspaceDelete_RemoveURLPath locks in the SDK's inverted
// Remove signature: Remove(ctx, workspaceID, params{FederationRuleID}) takes
// the workspace ID positionally and the federation rule ID via the params
// struct. Swapping them compiles (both are plain strings) and would only be
// caught at request time, against the wrong URL path — this test asserts the
// path is built correctly.
func TestFederationRuleWorkspaceDelete_RemoveURLPath(t *testing.T) {
	var requests []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"federation_rule_id": "fdrl_01ABC",
			"workspace_id":       "wrkspc_01XYZ",
			"type":               "federation_rule_workspace_deleted",
		}); err != nil {
			t.Errorf("encoding response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	r := &FederationRuleWorkspaceResource{client: newTestOAuthClient(srv)}

	var schemaResp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &schemaResp)

	schemaObjType := schemaType(t).(tftypes.Object)
	vals := nullValuesForSchema(t)
	vals["federation_rule_id"] = tftypes.NewValue(tftypes.String, "fdrl_01ABC")
	vals["workspace_id"] = tftypes.NewValue(tftypes.String, "wrkspc_01XYZ")
	rawVal := tftypes.NewValue(schemaObjType, vals)

	req := resource.DeleteRequest{State: tfsdk.State{Raw: rawVal, Schema: schemaResp.Schema}}
	resp := &resource.DeleteResponse{}

	r.Delete(context.Background(), req, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("Delete: %v", resp.Diagnostics)
	}

	if len(requests) != 1 {
		t.Fatalf("expected exactly one request, got %d: %v", len(requests), requests)
	}
	want := "DELETE /v1/organizations/federation_rules/fdrl_01ABC/workspaces/wrkspc_01XYZ"
	if requests[0] != want {
		t.Errorf("request = %q, want %q (federation_rule_id and workspace_id must not be swapped)", requests[0], want)
	}
}
