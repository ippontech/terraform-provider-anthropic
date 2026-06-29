// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package organizations

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/ippontech/terraform-provider-anthropic/internal/admintest"
)

func makeOrganizationMembersPage(members []organizationMemberAPIResponse, hasMore bool, lastID string) string {
	data, _ := json.Marshal(members)
	return fmt.Sprintf(`{"data":%s,"has_more":%v,"first_id":"","last_id":%q}`, data, hasMore, lastID)
}

func TestOrganizationMembersDataSource_listAll(t *testing.T) {
	members := []organizationMemberAPIResponse{
		{ID: "user_01A", Email: "a@example.com", Name: "Alice", Role: "admin", AddedAt: "2024-01-01T00:00:00Z", Type: "user"},
		{ID: "user_01B", Email: "b@example.com", Name: "Bob", Role: "developer", AddedAt: "2024-02-01T00:00:00Z", Type: "user"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/organizations/users" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, makeOrganizationMembersPage(members, false, ""))
	}))
	defer srv.Close()

	client := admintest.NewClient(t, srv)
	body, err := client.DoRequest(context.Background(), "GET", "/v1/organizations/users?limit=1000", nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	var page organizationMembersListResponse
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("parse: %v", err)
	}

	if len(page.Data) != 2 {
		t.Errorf("len(Data) = %d, want 2", len(page.Data))
	}
	if page.HasMore {
		t.Error("HasMore should be false")
	}
}

func TestOrganizationMembersDataSource_pagination(t *testing.T) {
	page1 := []organizationMemberAPIResponse{
		{ID: "user_01A", Email: "a@example.com", Name: "Alice", Role: "admin", Type: "user"},
	}
	page2 := []organizationMemberAPIResponse{
		{ID: "user_01B", Email: "b@example.com", Name: "Bob", Role: "developer", Type: "user"},
	}

	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		q := r.URL.Query()
		w.WriteHeader(http.StatusOK)
		if q.Get("after_id") == "" {
			_, _ = io.WriteString(w, makeOrganizationMembersPage(page1, true, "user_01A"))
		} else {
			_, _ = io.WriteString(w, makeOrganizationMembersPage(page2, false, ""))
		}
	}))
	defer srv.Close()

	client := admintest.NewClient(t, srv)

	var all []organizationMemberAPIResponse
	afterID := ""
	for {
		params := url.Values{}
		params.Set("limit", "1000")
		if afterID != "" {
			params.Set("after_id", afterID)
		}
		body, err := client.DoRequest(context.Background(), "GET", "/v1/organizations/users?"+params.Encode(), nil)
		if err != nil {
			t.Fatalf("page request: %v", err)
		}
		var page organizationMembersListResponse
		if err := json.Unmarshal(body, &page); err != nil {
			t.Fatalf("parse: %v", err)
		}
		all = append(all, page.Data...)
		if !page.HasMore {
			break
		}
		afterID = page.LastID
	}

	if len(all) != 2 {
		t.Errorf("total members = %d, want 2", len(all))
	}
	if callCount != 2 {
		t.Errorf("callCount = %d, want 2", callCount)
	}
}

func TestOrganizationMembersDataSource_buildQuery(t *testing.T) {
	t.Run("email and cursor set", func(t *testing.T) {
		q := buildOrganizationMembersQuery("user_01A", "jane@example.com")
		if got := q.Get("limit"); got != "1000" {
			t.Errorf("limit = %q, want 1000", got)
		}
		if got := q.Get("after_id"); got != "user_01A" {
			t.Errorf("after_id = %q, want user_01A", got)
		}
		if got := q.Get("email"); got != "jane@example.com" {
			t.Errorf("email = %q, want jane@example.com", got)
		}
	})

	t.Run("no email, no cursor", func(t *testing.T) {
		q := buildOrganizationMembersQuery("", "")
		if got := q.Get("limit"); got != "1000" {
			t.Errorf("limit = %q, want 1000", got)
		}
		if _, ok := q["after_id"]; ok {
			t.Errorf("after_id should be omitted when empty, got %q", q.Get("after_id"))
		}
		if _, ok := q["email"]; ok {
			t.Errorf("email should be omitted when empty, got %q", q.Get("email"))
		}
	})
}
