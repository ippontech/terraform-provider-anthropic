// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package serviceaccounts

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-framework/types"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// newServiceAccountWorkspacesTestClient builds an OAuth-wrapped SDK client
// pointed at an httptest server for CI-deterministic unit tests. Named
// distinctly from any equivalent helper a sibling WIF branch (e.g. the
// anthropic_service_account_workspace resource) might add to this same
// package, so the two can coexist once both branches merge.
func newServiceAccountWorkspacesTestClient(t *testing.T, srv *httptest.Server) *providerdata.OAuthClient {
	t.Helper()
	c := anthropic.NewClient(option.WithBaseURL(srv.URL), option.WithAuthToken("test"))
	return &providerdata.OAuthClient{Client: &c}
}

// --- mapServiceAccountWorkspacesListEntry ---

func TestMapServiceAccountWorkspacesListEntry_Explicit(t *testing.T) {
	member := &anthropic.BetaServiceAccountWorkspaceMember{
		ServiceAccountID: "svac_01ABC",
		WorkspaceID:      "wrkspc_01XYZ",
		WorkspaceRole:    anthropic.BetaWorkspaceRoleWorkspaceDeveloper,
		Implicit:         false,
		CreatedByActorID: "user_01CREATOR",
	}

	obj, diags := mapServiceAccountWorkspacesListEntry(member)
	if diags.HasError() {
		t.Fatalf("mapServiceAccountWorkspacesListEntry returned errors: %+v", diags)
	}

	typed, ok := obj.(types.Object)
	if !ok {
		t.Fatalf("expected types.Object, got %T", obj)
	}
	attrs := typed.Attributes()

	if got := attrs["workspace_id"].(types.String).ValueString(); got != "wrkspc_01XYZ" {
		t.Errorf("workspace_id = %q, want wrkspc_01XYZ", got)
	}
	if got := attrs["workspace_role"].(types.String).ValueString(); got != "workspace_developer" {
		t.Errorf("workspace_role = %q, want workspace_developer", got)
	}
	if attrs["implicit"].(types.Bool).ValueBool() {
		t.Errorf("implicit = true, want false")
	}
	if got := attrs["created_by_actor_id"].(types.String).ValueString(); got != "user_01CREATOR" {
		t.Errorf("created_by_actor_id = %q, want user_01CREATOR", got)
	}
}

func TestMapServiceAccountWorkspacesListEntry_EmptyActorID(t *testing.T) {
	member := &anthropic.BetaServiceAccountWorkspaceMember{
		ServiceAccountID: "svac_01ABC",
		WorkspaceID:      "wrkspc_01XYZ",
		WorkspaceRole:    anthropic.BetaWorkspaceRoleWorkspaceUser,
		Implicit:         true,
		CreatedByActorID: "",
	}

	obj, diags := mapServiceAccountWorkspacesListEntry(member)
	if diags.HasError() {
		t.Fatalf("mapServiceAccountWorkspacesListEntry returned errors: %+v", diags)
	}

	attrs := obj.(types.Object).Attributes()
	if !attrs["created_by_actor_id"].(types.String).IsNull() {
		t.Errorf("created_by_actor_id = %v, want null for an empty actor ID", attrs["created_by_actor_id"])
	}
}

func TestMapServiceAccountWorkspacesListEntry_Implicit(t *testing.T) {
	member := &anthropic.BetaServiceAccountWorkspaceMember{
		ServiceAccountID: "svac_01ABC",
		WorkspaceID:      "wrkspc_default",
		WorkspaceRole:    anthropic.BetaWorkspaceRoleWorkspaceUser,
		Implicit:         true,
		CreatedByActorID: "svac_01ABC",
	}

	obj, diags := mapServiceAccountWorkspacesListEntry(member)
	if diags.HasError() {
		t.Fatalf("mapServiceAccountWorkspacesListEntry returned errors: %+v", diags)
	}
	if obj.IsNull() || obj.IsUnknown() {
		t.Fatalf("expected a known, non-null object")
	}
}

// --- Pagination ---

func makeServiceAccountWorkspacesPage(members []anthropic.BetaServiceAccountWorkspaceMember, nextPage string) string {
	data, _ := json.Marshal(members)
	return fmt.Sprintf(`{"data":%s,"next_page":%s}`, data, jsonStringOrNull(nextPage))
}

func jsonStringOrNull(s string) string {
	if s == "" {
		return "null"
	}
	b, _ := json.Marshal(s)
	return string(b)
}

func TestServiceAccountWorkspacesDataSource_pagination(t *testing.T) {
	page1 := []anthropic.BetaServiceAccountWorkspaceMember{
		{ServiceAccountID: "svac_01ABC", WorkspaceID: "wrkspc_default", WorkspaceRole: anthropic.BetaWorkspaceRoleWorkspaceUser, Implicit: true, CreatedByActorID: "svac_01ABC"},
	}
	page2 := []anthropic.BetaServiceAccountWorkspaceMember{
		{ServiceAccountID: "svac_01ABC", WorkspaceID: "wrkspc_01XYZ", WorkspaceRole: anthropic.BetaWorkspaceRoleWorkspaceDeveloper, Implicit: false, CreatedByActorID: "user_01CREATOR"},
	}

	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if r.Method != http.MethodGet {
			http.Error(w, "unexpected method", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if r.URL.Query().Get("page") == "" {
			_, _ = io.WriteString(w, makeServiceAccountWorkspacesPage(page1, "cursor_page2"))
		} else {
			_, _ = io.WriteString(w, makeServiceAccountWorkspacesPage(page2, ""))
		}
	}))
	defer srv.Close()

	client := newServiceAccountWorkspacesTestClient(t, srv)

	pager := client.Beta.Organization.ServiceAccounts.Workspaces.ListAutoPaging(context.Background(), "svac_01ABC", anthropic.BetaOrganizationServiceAccountWorkspaceListParams{})

	var all []anthropic.BetaServiceAccountWorkspaceMember
	for pager.Next() {
		all = append(all, pager.Current())
	}
	if err := pager.Err(); err != nil {
		t.Fatalf("pager.Err() = %v, want nil", err)
	}

	if len(all) != 2 {
		t.Fatalf("total workspaces = %d, want 2", len(all))
	}
	if callCount != 2 {
		t.Errorf("callCount = %d, want 2", callCount)
	}
	if !all[0].Implicit {
		t.Errorf("first entry Implicit = false, want true")
	}
	if all[1].Implicit {
		t.Errorf("second entry Implicit = true, want false")
	}
	if all[1].WorkspaceRole != anthropic.BetaWorkspaceRoleWorkspaceDeveloper {
		t.Errorf("second entry WorkspaceRole = %q, want workspace_developer", all[1].WorkspaceRole)
	}
}

// --- 404 on unknown service account ---

func TestServiceAccountWorkspacesDataSource_notFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"not_found_error","message":"service account not found"}}`)
	}))
	defer srv.Close()

	client := newServiceAccountWorkspacesTestClient(t, srv)

	pager := client.Beta.Organization.ServiceAccounts.Workspaces.ListAutoPaging(context.Background(), "svac_doesnotexist", anthropic.BetaOrganizationServiceAccountWorkspaceListParams{})

	for pager.Next() {
		t.Fatalf("expected no entries from a 404 response, got %+v", pager.Current())
	}
	if err := pager.Err(); err == nil {
		t.Fatal("pager.Err() = nil, want a 404 error")
	}
}
