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

	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
)

func workspaceMemberFixture() string {
	return `{
		"user_id": "user_01xyz789",
		"workspace_role": "workspace_user",
		"type": "workspace_member"
	}`
}

func TestWorkspaceMemberCreate_parsesResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/v1/organizations/workspaces/ws_01abc123/members" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, workspaceMemberFixture())
	}))
	defer srv.Close()

	client := newTestAdminClient(t, srv)
	body, err := client.DoRequest(context.Background(), "POST", "/v1/organizations/workspaces/ws_01abc123/members",
		workspaceMemberCreateRequest{UserID: "user_01xyz789", WorkspaceRole: "workspace_user"})
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	var member workspaceMemberAPIResponse
	if err := json.Unmarshal(body, &member); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if member.UserID != "user_01xyz789" {
		t.Errorf("UserID = %q, want %q", member.UserID, "user_01xyz789")
	}
	if member.WorkspaceRole != "workspace_user" {
		t.Errorf("WorkspaceRole = %q, want %q", member.WorkspaceRole, "workspace_user")
	}
	if member.Type != "workspace_member" {
		t.Errorf("Type = %q, want %q", member.Type, "workspace_member")
	}
}

func TestWorkspaceMemberRead_notFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"type":"not_found_error","message":"workspace member not found"}}`)
	}))
	defer srv.Close()

	client := newTestAdminClient(t, srv)
	_, err := client.DoRequest(context.Background(), "GET",
		"/v1/organizations/workspaces/ws_01abc123/members/user_missing", nil)
	if !admin.IsNotFound(err) {
		t.Fatalf("expected IsNotFound, got: %v", err)
	}
}
