// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package organizations

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	"github.com/ippontech/terraform-provider-anthropic/internal/admintest"
)

const inviteFixture = `{
	"id": "invite_015gWxCN9Hfg2QhZwTK7",
	"email": "jane@example.com",
	"role": "developer",
	"status": "pending",
	"invited_at": "2024-10-30T23:58:27.427722Z",
	"expires_at": "2024-11-06T23:58:27.427722Z",
	"accepted_at": "",
	"type": "invite"
}`

func TestInviteDataSource_read(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/organizations/invites/invite_015gWxCN9Hfg2QhZwTK7" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		if r.URL.Query().Get("beta") != "true" {
			http.Error(w, "missing beta=true", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, inviteFixture)
	}))
	defer srv.Close()

	client := admintest.NewClient(t, srv)

	body, err := client.DoRequest(context.Background(), "GET", "/v1/organizations/invites/invite_015gWxCN9Hfg2QhZwTK7?beta=true", nil)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var invite inviteAPIResponse
	if err := json.Unmarshal(body, &invite); err != nil {
		t.Fatalf("parse: %v", err)
	}

	data := mapInviteToState(invite)

	if got := data.ID.ValueString(); got != "invite_015gWxCN9Hfg2QhZwTK7" {
		t.Errorf("ID = %q, want %q", got, "invite_015gWxCN9Hfg2QhZwTK7")
	}
	if got := data.Email.ValueString(); got != "jane@example.com" {
		t.Errorf("Email = %q, want %q", got, "jane@example.com")
	}
	if got := data.Role.ValueString(); got != "developer" {
		t.Errorf("Role = %q, want %q", got, "developer")
	}
	if got := data.Status.ValueString(); got != "pending" {
		t.Errorf("Status = %q, want %q", got, "pending")
	}
	if got := data.InvitedAt.ValueString(); got != "2024-10-30T23:58:27.427722Z" {
		t.Errorf("InvitedAt = %q, want %q", got, "2024-10-30T23:58:27.427722Z")
	}
	if got := data.ExpiresAt.ValueString(); got != "2024-11-06T23:58:27.427722Z" {
		t.Errorf("ExpiresAt = %q, want %q", got, "2024-11-06T23:58:27.427722Z")
	}
	if got := data.AcceptedAt.ValueString(); got != "" {
		t.Errorf("AcceptedAt = %q, want empty", got)
	}
	if got := data.Type.ValueString(); got != "invite" {
		t.Errorf("Type = %q, want %q", got, "invite")
	}
}

func TestInviteDataSource_notFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"type":"not_found_error","message":"invite not found"}}`)
	}))
	defer srv.Close()

	client := admintest.NewClient(t, srv)

	_, err := client.DoRequest(context.Background(), "GET", "/v1/organizations/invites/invite_missing?beta=true", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !admin.IsNotFound(err) {
		t.Fatalf("expected 404 not-found error, got: %v", err)
	}
}
