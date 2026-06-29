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

const organizationMemberFixture = `{
	"id": "user_01WCz1FkmYMm4gnmykNKUu3Q",
	"added_at": "2024-10-30T23:58:27.427722Z",
	"email": "jane@example.com",
	"name": "Jane Doe",
	"role": "developer",
	"type": "user"
}`

func TestOrganizationMemberDataSource_read(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/organizations/users/user_01WCz1FkmYMm4gnmykNKUu3Q" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, organizationMemberFixture)
	}))
	defer srv.Close()

	client := admintest.NewClient(t, srv)

	body, err := client.DoRequest(context.Background(), "GET", "/v1/organizations/users/user_01WCz1FkmYMm4gnmykNKUu3Q", nil)
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	var member organizationMemberAPIResponse
	if err := json.Unmarshal(body, &member); err != nil {
		t.Fatalf("parse: %v", err)
	}

	data := mapOrganizationMemberToState(member)

	if got := data.ID.ValueString(); got != "user_01WCz1FkmYMm4gnmykNKUu3Q" {
		t.Errorf("ID = %q, want %q", got, "user_01WCz1FkmYMm4gnmykNKUu3Q")
	}
	if got := data.Email.ValueString(); got != "jane@example.com" {
		t.Errorf("Email = %q, want %q", got, "jane@example.com")
	}
	if got := data.Name.ValueString(); got != "Jane Doe" {
		t.Errorf("Name = %q, want %q", got, "Jane Doe")
	}
	if got := data.Role.ValueString(); got != "developer" {
		t.Errorf("Role = %q, want %q", got, "developer")
	}
	if got := data.AddedAt.ValueString(); got != "2024-10-30T23:58:27.427722Z" {
		t.Errorf("AddedAt = %q, want %q", got, "2024-10-30T23:58:27.427722Z")
	}
	if got := data.Type.ValueString(); got != "user" {
		t.Errorf("Type = %q, want %q", got, "user")
	}
}

func TestOrganizationMemberDataSource_notFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"type":"not_found_error","message":"user not found"}}`)
	}))
	defer srv.Close()

	client := admintest.NewClient(t, srv)

	_, err := client.DoRequest(context.Background(), "GET", "/v1/organizations/users/user_missing", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !admin.IsNotFound(err) {
		t.Fatalf("expected 404 not-found error, got: %v", err)
	}
}
