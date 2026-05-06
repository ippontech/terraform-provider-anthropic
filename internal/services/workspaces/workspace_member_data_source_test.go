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

func TestWorkspaceMemberDataSource_Read_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/organizations/workspaces/wrkspc_01ABC/members/user_01XYZ" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"workspace_role":"workspace_developer","type":"workspace_member"}`)
	}))
	defer srv.Close()

	client := newTestAdminClient(t, srv)
	body, err := client.DoRequest(context.Background(), "GET",
		"/v1/organizations/workspaces/wrkspc_01ABC/members/user_01XYZ", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var m workspaceMemberAPIResponse
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if m.WorkspaceRole != "workspace_developer" {
		t.Errorf("WorkspaceRole = %q, want workspace_developer", m.WorkspaceRole)
	}
	if m.Type != "workspace_member" {
		t.Errorf("Type = %q, want workspace_member", m.Type)
	}
}

func TestWorkspaceMemberDataSource_Read_pathConstruction(t *testing.T) {
	tests := []struct {
		workspaceID string
		userID      string
		wantPath    string
	}{
		{"wrkspc_01ABC", "user_01XYZ", "/v1/organizations/workspaces/wrkspc_01ABC/members/user_01XYZ"},
		{"wrkspc_99ZZZ", "user_99AAA", "/v1/organizations/workspaces/wrkspc_99ZZZ/members/user_99AAA"},
	}

	for _, tc := range tests {
		t.Run(tc.wantPath, func(t *testing.T) {
			var gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, `{"workspace_role":"workspace_admin","type":"workspace_member"}`)
			}))
			defer srv.Close()

			client := newTestAdminClient(t, srv)
			path := "/v1/organizations/workspaces/" + tc.workspaceID + "/members/" + tc.userID
			_, err := client.DoRequest(context.Background(), "GET", path, nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotPath != tc.wantPath {
				t.Errorf("path = %q, want %q", gotPath, tc.wantPath)
			}
		})
	}
}

func TestWorkspaceMemberDataSource_Read_notFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"type":"not_found_error","message":"workspace member not found"}}`)
	}))
	defer srv.Close()

	client := newTestAdminClient(t, srv)
	_, err := client.DoRequest(context.Background(), "GET",
		"/v1/organizations/workspaces/wrkspc_01ABC/members/user_missing", nil)
	if !admin.IsNotFound(err) {
		t.Fatalf("expected IsNotFound, got: %v", err)
	}
}

func TestWorkspaceMemberDataSource_Read_allRoles(t *testing.T) {
	roles := []string{"workspace_developer", "workspace_admin", "workspace_billing"}
	for _, role := range roles {
		t.Run(role, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, `{"workspace_role":"`+role+`","type":"workspace_member"}`)
			}))
			defer srv.Close()

			client := newTestAdminClient(t, srv)
			body, err := client.DoRequest(context.Background(), "GET",
				"/v1/organizations/workspaces/wrkspc_01ABC/members/user_01XYZ", nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			var m workspaceMemberAPIResponse
			if err := json.Unmarshal(body, &m); err != nil {
				t.Fatalf("parse: %v", err)
			}
			if m.WorkspaceRole != role {
				t.Errorf("WorkspaceRole = %q, want %q", m.WorkspaceRole, role)
			}
		})
	}
}
