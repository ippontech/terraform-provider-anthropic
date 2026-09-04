// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// newTestFederationOAuthClient builds an SDK client pointed at an httptest
// server, authenticated with a bearer token the way pd.OAuthClient is in
// production. WithoutEnvironmentDefaults keeps the test hermetic: it must not
// pick up ambient ANTHROPIC_API_KEY / ANTHROPIC_AUTH_TOKEN from the test
// process environment.
func newTestFederationOAuthClient(t *testing.T, srv *httptest.Server) *anthropic.Client {
	t.Helper()
	c := anthropic.NewClient(
		option.WithoutEnvironmentDefaults(),
		option.WithBaseURL(srv.URL),
		option.WithAuthToken("test-oauth-token"),
	)
	return &c
}

func federationRuleWorkspaceFixture(workspaceID, workspaceName, createdAt, createdByActorID string) map[string]any {
	return map[string]any{
		"type":                "federation_rule_workspace",
		"federation_rule_id":  "fdrl_01ABC",
		"workspace_id":        workspaceID,
		"workspace_name":      workspaceName,
		"created_at":          createdAt,
		"created_by_actor_id": createdByActorID,
	}
}

// --- mapping ---

func TestMapFederationRuleWorkspacesListItem(t *testing.T) {
	createdAt := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	w := &anthropic.BetaFederationRuleWorkspace{
		FederationRuleID: "fdrl_01ABC",
		WorkspaceID:      "wrkspc_01WS",
		WorkspaceName:    "prod",
		CreatedAt:        createdAt,
		CreatedByActorID: "user_01XYZ",
	}

	obj, diags := mapFederationRuleWorkspacesListItem(w)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	objVal, ok := obj.(types.Object)
	if !ok {
		t.Fatalf("expected types.Object, got %T", obj)
	}
	attrs := objVal.Attributes()

	if got := attrs["workspace_id"].(types.String).ValueString(); got != "wrkspc_01WS" {
		t.Errorf("workspace_id = %q, want %q", got, "wrkspc_01WS")
	}
	if got := attrs["workspace_name"].(types.String).ValueString(); got != "prod" {
		t.Errorf("workspace_name = %q, want %q", got, "prod")
	}
	if got := attrs["created_at"].(types.String).ValueString(); got != "2024-01-15T10:00:00Z" {
		t.Errorf("created_at = %q, want %q", got, "2024-01-15T10:00:00Z")
	}
	if got := attrs["created_by_actor_id"].(types.String).ValueString(); got != "user_01XYZ" {
		t.Errorf("created_by_actor_id = %q, want %q", got, "user_01XYZ")
	}
}

func TestMapFederationRuleWorkspacesListItem_emptyCreatedByActorID(t *testing.T) {
	w := &anthropic.BetaFederationRuleWorkspace{
		FederationRuleID: "fdrl_01ABC",
		WorkspaceID:      "wrkspc_01WS",
		WorkspaceName:    "prod",
		CreatedAt:        time.Now(),
		CreatedByActorID: "",
	}

	obj, diags := mapFederationRuleWorkspacesListItem(w)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if obj.IsNull() || obj.IsUnknown() {
		t.Fatalf("expected a known, non-null object")
	}
}

// --- Read: single page ---

func TestFederationRuleWorkspacesDataSource_singlePage(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		if r.Method != http.MethodGet {
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				federationRuleWorkspaceFixture("wrkspc_01", "ws-one", "2024-01-15T10:00:00Z", "user_01A"),
				federationRuleWorkspaceFixture("wrkspc_02", "ws-two", "2024-01-16T10:00:00Z", "svac_01B"),
			},
			"next_page": nil,
		})
	}))
	defer srv.Close()

	client := newTestFederationOAuthClient(t, srv)

	pager := client.Beta.Organization.Federation.Rules.Workspaces.ListAutoPaging(
		context.Background(), "fdrl_01ABC", anthropic.BetaOrganizationFederationRuleWorkspaceListParams{},
	)

	var got []anthropic.BetaFederationRuleWorkspace
	for pager.Next() {
		got = append(got, pager.Current())
	}
	if err := pager.Err(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0].WorkspaceID != "wrkspc_01" || got[1].WorkspaceID != "wrkspc_02" {
		t.Errorf("unexpected workspace IDs: %+v", got)
	}

	wantPath := "/v1/organizations/federation_rules/fdrl_01ABC/workspaces"
	if gotPath != wantPath {
		t.Errorf("path = %q, want %q", gotPath, wantPath)
	}
}

// --- Read: pagination ---
//
// The live API for this endpoint always returns every enablement in a single
// response (next_page is always null), but the SDK's PageCursorAutoPager is
// generic, so this test exercises it against a synthetic two-page response to
// cover the code path defensively.
func TestFederationRuleWorkspacesDataSource_pagination(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					federationRuleWorkspaceFixture("wrkspc_01", "ws-one", "2024-01-15T10:00:00Z", "user_01A"),
				},
				"next_page": "cursor-2",
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				federationRuleWorkspaceFixture("wrkspc_02", "ws-two", "2024-01-16T10:00:00Z", "svac_01B"),
			},
			"next_page": nil,
		})
	}))
	defer srv.Close()

	client := newTestFederationOAuthClient(t, srv)

	pager := client.Beta.Organization.Federation.Rules.Workspaces.ListAutoPaging(
		context.Background(), "fdrl_01ABC", anthropic.BetaOrganizationFederationRuleWorkspaceListParams{},
	)

	var got []anthropic.BetaFederationRuleWorkspace
	for pager.Next() {
		got = append(got, pager.Current())
	}
	if err := pager.Err(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if callCount != 2 {
		t.Errorf("callCount = %d, want 2", callCount)
	}
	if len(got) != 2 {
		t.Fatalf("len(got) = %d, want 2", len(got))
	}
	if got[0].WorkspaceID != "wrkspc_01" || got[1].WorkspaceID != "wrkspc_02" {
		t.Errorf("unexpected workspace IDs across pages: %+v", got)
	}
}

// --- Read: 404 on unknown rule ---

func TestFederationRuleWorkspacesDataSource_notFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"type": "error",
			"error": map[string]any{
				"type":    "not_found_error",
				"message": "federation rule not found",
			},
		})
	}))
	defer srv.Close()

	client := newTestFederationOAuthClient(t, srv)

	pager := client.Beta.Organization.Federation.Rules.Workspaces.ListAutoPaging(
		context.Background(), "fdrl_missing", anthropic.BetaOrganizationFederationRuleWorkspaceListParams{},
	)

	for pager.Next() { //nolint:revive // draining the pager is how errors surface
	}
	if err := pager.Err(); err == nil {
		t.Fatal("expected an error for a 404 response, got nil")
	}
}
