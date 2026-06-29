// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWorkspaceMembersDataSource_paginatesTransparently(t *testing.T) {
	page1 := `{
		"data": [
			{"user_id": "user_01", "workspace_id": "ws_01", "workspace_role": "workspace_admin", "type": "workspace_member"},
			{"user_id": "user_02", "workspace_id": "ws_01", "workspace_role": "workspace_developer", "type": "workspace_member"}
		],
		"has_more": true,
		"first_id": "user_01",
		"last_id": "user_02"
	}`
	page2 := `{
		"data": [
			{"user_id": "user_03", "workspace_id": "ws_01", "workspace_role": "workspace_developer", "type": "workspace_member"}
		],
		"has_more": false,
		"first_id": "user_03",
		"last_id": "user_03"
	}`

	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("after_id") == "" {
			_, _ = io.WriteString(w, page1)
		} else {
			_, _ = io.WriteString(w, page2)
		}
	}))
	defer srv.Close()

	client := newTestAdminClient(t, srv)

	allMembers := fetchAllPages(t, client, "/v1/organizations/workspaces/ws_01/members?limit=1000",
		func(b []byte) (pageData[workspaceMembersListItem], error) {
			var p workspaceMembersListResponse
			if err := json.Unmarshal(b, &p); err != nil {
				return pageData[workspaceMembersListItem]{}, err
			}
			return pageData[workspaceMembersListItem]{data: p.Data, hasMore: p.HasMore, lastID: p.LastID}, nil
		})

	if callCount != 2 {
		t.Errorf("expected 2 HTTP calls, got %d", callCount)
	}
	if len(allMembers) != 3 {
		t.Fatalf("expected 3 members, got %d", len(allMembers))
	}
	if allMembers[0].UserID != "user_01" {
		t.Errorf("members[0].UserID = %q, want user_01", allMembers[0].UserID)
	}
	if allMembers[0].WorkspaceRole != "workspace_admin" {
		t.Errorf("members[0].WorkspaceRole = %q, want workspace_admin", allMembers[0].WorkspaceRole)
	}
	if allMembers[2].UserID != "user_03" {
		t.Errorf("members[2].UserID = %q, want user_03", allMembers[2].UserID)
	}
}

func TestWorkspaceMembersDataSource_emptyWorkspace(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[],"has_more":false,"first_id":"","last_id":""}`)
	}))
	defer srv.Close()

	client := newTestAdminClient(t, srv)

	respBytes, err := client.DoRequest(context.Background(), "GET", "/v1/organizations/workspaces/ws_empty/members?limit=1000", nil)
	if err != nil {
		t.Fatalf("DoRequest: %v", err)
	}
	var page workspaceMembersListResponse
	if err := json.Unmarshal(respBytes, &page); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(page.Data) != 0 {
		t.Errorf("expected 0 members, got %d", len(page.Data))
	}
	if page.HasMore {
		t.Error("expected has_more=false for empty workspace")
	}
}
