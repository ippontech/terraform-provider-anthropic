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

func makeInvitesPage(invites []inviteAPIResponse, hasMore bool, lastID string) string {
	data, _ := json.Marshal(invites)
	return fmt.Sprintf(`{"data":%s,"has_more":%v,"first_id":"","last_id":%q}`, data, hasMore, lastID)
}

func TestInvitesDataSource_listAll(t *testing.T) {
	invites := []inviteAPIResponse{
		{ID: "invite_01A", Email: "a@example.com", Role: "developer", Status: "pending", InvitedAt: "2024-01-01T00:00:00Z", ExpiresAt: "2024-01-08T00:00:00Z", Type: "invite"},
		{ID: "invite_01B", Email: "b@example.com", Role: "user", Status: "accepted", InvitedAt: "2024-02-01T00:00:00Z", ExpiresAt: "2024-02-08T00:00:00Z", AcceptedAt: "2024-02-02T00:00:00Z", Type: "invite"},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/v1/organizations/invites" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		if r.URL.Query().Get("beta") != "true" {
			http.Error(w, "missing beta=true", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, makeInvitesPage(invites, false, ""))
	}))
	defer srv.Close()

	client := admintest.NewClient(t, srv)
	body, err := client.DoRequest(context.Background(), "GET", "/v1/organizations/invites?"+buildInvitesQuery("", "", nil).Encode(), nil)
	if err != nil {
		t.Fatalf("list: %v", err)
	}

	var page invitesListResponse
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

func TestInvitesDataSource_pagination(t *testing.T) {
	page1 := []inviteAPIResponse{
		{ID: "invite_01A", Email: "a@example.com", Role: "developer", Status: "pending", Type: "invite"},
	}
	page2 := []inviteAPIResponse{
		{ID: "invite_01B", Email: "b@example.com", Role: "user", Status: "accepted", Type: "invite"},
	}

	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		q := r.URL.Query()
		w.WriteHeader(http.StatusOK)
		if q.Get("after_id") == "" {
			_, _ = io.WriteString(w, makeInvitesPage(page1, true, "invite_01A"))
		} else {
			_, _ = io.WriteString(w, makeInvitesPage(page2, false, ""))
		}
	}))
	defer srv.Close()

	client := admintest.NewClient(t, srv)

	var all []inviteAPIResponse
	afterID := ""
	for {
		body, err := client.DoRequest(context.Background(), "GET", "/v1/organizations/invites?"+buildInvitesQuery(afterID, "", nil).Encode(), nil)
		if err != nil {
			t.Fatalf("page request: %v", err)
		}
		var page invitesListResponse
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
		t.Errorf("total invites = %d, want 2", len(all))
	}
	if callCount != 2 {
		t.Errorf("callCount = %d, want 2", callCount)
	}
}

func TestInvitesDataSource_buildQuery(t *testing.T) {
	t.Run("email, cursor, and statuses set", func(t *testing.T) {
		q := buildInvitesQuery("invite_01A", "jane@example.com", []string{"pending", "expired"})
		if got := q.Get("beta"); got != "true" {
			t.Errorf("beta = %q, want true", got)
		}
		if got := q.Get("limit"); got != "1000" {
			t.Errorf("limit = %q, want 1000", got)
		}
		if got := q.Get("after_id"); got != "invite_01A" {
			t.Errorf("after_id = %q, want invite_01A", got)
		}
		if got := q.Get("email"); got != "jane@example.com" {
			t.Errorf("email = %q, want jane@example.com", got)
		}
		if got := (url.Values)(q)["statuses"]; len(got) != 2 || got[0] != "pending" || got[1] != "expired" {
			t.Errorf("statuses = %v, want [pending expired]", got)
		}
	})

	t.Run("no email, no cursor, no statuses", func(t *testing.T) {
		q := buildInvitesQuery("", "", nil)
		if got := q.Get("beta"); got != "true" {
			t.Errorf("beta = %q, want true", got)
		}
		if got := q.Get("limit"); got != "1000" {
			t.Errorf("limit = %q, want 1000", got)
		}
		if _, ok := q["after_id"]; ok {
			t.Errorf("after_id should be omitted when empty, got %q", q.Get("after_id"))
		}
		if _, ok := q["email"]; ok {
			t.Errorf("email should be omitted when empty, got %q", q.Get("email"))
		}
		if _, ok := q["statuses"]; ok {
			t.Errorf("statuses should be omitted when empty, got %v", q["statuses"])
		}
	})
}
