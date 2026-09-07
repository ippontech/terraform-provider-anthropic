// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package serviceaccounts

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
)

// serviceAccountFixture builds a minimal, valid BetaServiceAccount JSON payload
// for the given id/name, optionally archived.
func serviceAccountFixture(id, name string, archived bool) map[string]any {
	archivedAt := any(nil)
	archivedBy := ""
	if archived {
		archivedAt = "2026-06-01T00:00:00Z"
		archivedBy = "user_01ARCHIVER"
	}
	return map[string]any{
		"id":                   id,
		"type":                 "service_account",
		"name":                 name,
		"description":          "",
		"organization_role":    "developer",
		"created_at":           "2026-01-01T00:00:00Z",
		"updated_at":           "2026-01-02T00:00:00Z",
		"created_by_actor_id":  "user_01CREATOR",
		"updated_by_actor_id":  "user_01UPDATER",
		"archived_at":          archivedAt,
		"archived_by_actor_id": archivedBy,
	}
}

func newTestServiceAccountsClient(t *testing.T, srv *httptest.Server) *anthropic.Client {
	t.Helper()
	c := anthropic.NewClient(option.WithBaseURL(srv.URL), option.WithAuthToken("test"))
	return &c
}

// TestServiceAccountsDataSource_pagination verifies the SDK's ListAutoPaging
// follows the "next_page" cursor across multiple pages until it is empty.
func TestServiceAccountsDataSource_pagination(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		page := r.URL.Query().Get("page")
		var body map[string]any
		switch page {
		case "":
			body = map[string]any{
				"data":      []any{serviceAccountFixture("svac_01", "first", false)},
				"next_page": "cursor-2",
			}
		case "cursor-2":
			body = map[string]any{
				"data":      []any{serviceAccountFixture("svac_02", "second", false)},
				"next_page": nil,
			}
		default:
			t.Fatalf("unexpected page cursor %q", page)
		}
		if err := json.NewEncoder(w).Encode(body); err != nil {
			t.Errorf("encoding response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	client := newTestServiceAccountsClient(t, srv)

	pager := client.Beta.Organization.ServiceAccounts.ListAutoPaging(t.Context(), anthropic.BetaOrganizationServiceAccountListParams{})

	var got []anthropic.BetaServiceAccount
	for pager.Next() {
		got = append(got, pager.Current())
	}
	if err := pager.Err(); err != nil {
		t.Fatalf("unexpected pager error: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("want 2 service accounts across pages, got %d", len(got))
	}
	if got[0].ID != "svac_01" || got[1].ID != "svac_02" {
		t.Errorf("unexpected IDs: %q, %q", got[0].ID, got[1].ID)
	}
	if calls != 2 {
		t.Errorf("want 2 HTTP calls for pagination, got %d", calls)
	}
}

// TestServiceAccountsDataSource_emptyList covers the case where the
// organization has no service accounts at all.
func TestServiceAccountsDataSource_emptyList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"data":      []any{},
			"next_page": nil,
		}); err != nil {
			t.Errorf("encoding response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	client := newTestServiceAccountsClient(t, srv)

	pager := client.Beta.Organization.ServiceAccounts.ListAutoPaging(t.Context(), anthropic.BetaOrganizationServiceAccountListParams{})

	var got []anthropic.BetaServiceAccount
	for pager.Next() {
		got = append(got, pager.Current())
	}
	if err := pager.Err(); err != nil {
		t.Fatalf("unexpected pager error: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want 0 service accounts, got %d", len(got))
	}
}

// TestServiceAccountsDataSource_includeArchivedQueryParam verifies that
// setting IncludeArchived sends the query parameter, and that leaving it unset
// omits it (letting the API apply its own false default).
func TestServiceAccountsDataSource_includeArchivedQueryParam(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(map[string]any{
			"data":      []any{},
			"next_page": nil,
		}); err != nil {
			t.Errorf("encoding response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	client := newTestServiceAccountsClient(t, srv)

	pager := client.Beta.Organization.ServiceAccounts.ListAutoPaging(t.Context(), anthropic.BetaOrganizationServiceAccountListParams{
		IncludeArchived: param.NewOpt(true),
	})
	for pager.Next() { //nolint:revive // draining the pager is the point of the test
	}
	if err := pager.Err(); err != nil {
		t.Fatalf("unexpected pager error: %v", err)
	}

	if got := gotQuery; got == "" || !strings.Contains(got, "include_archived=true") {
		t.Errorf("expected query to contain include_archived=true, got %q", got)
	}

	// Now without setting it at all.
	pager = client.Beta.Organization.ServiceAccounts.ListAutoPaging(t.Context(), anthropic.BetaOrganizationServiceAccountListParams{})
	for pager.Next() { //nolint:revive // draining the pager is the point of the test
	}
	if err := pager.Err(); err != nil {
		t.Fatalf("unexpected pager error: %v", err)
	}
	if strings.Contains(gotQuery, "include_archived") {
		t.Errorf("expected query to omit include_archived when unset, got %q", gotQuery)
	}
}

func TestMapServiceAccountsListEntry_basicFields(t *testing.T) {
	createdAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)

	sa := &anthropic.BetaServiceAccount{
		ID:               "svac_01ABC",
		Name:             "ci-runner",
		Description:      "runs CI jobs",
		OrganizationRole: anthropic.BetaServiceAccountOrganizationRoleDeveloper,
		CreatedAt:        createdAt,
		UpdatedAt:        updatedAt,
		CreatedByActorID: "user_01CREATOR",
		UpdatedByActorID: "user_01UPDATER",
	}

	obj, diags := mapServiceAccountsListEntry(sa)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if obj.IsNull() || obj.IsUnknown() {
		t.Fatal("expected a known, non-null object")
	}
}

func TestMapServiceAccountsListEntry_archivedAtZero(t *testing.T) {
	sa := &anthropic.BetaServiceAccount{
		ID:        "svac_02DEF",
		Name:      "unarchived",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		// ArchivedAt left at zero value (not archived).
	}

	_, diags := mapServiceAccountsListEntry(sa)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
}

func TestMapServiceAccountsListEntry_archivedAtNonZero(t *testing.T) {
	archivedAt := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	sa := &anthropic.BetaServiceAccount{
		ID:                "svac_03GHI",
		Name:              "archived-account",
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
		ArchivedAt:        archivedAt,
		ArchivedByActorID: "user_01ARCHIVER",
		OrganizationRole:  anthropic.BetaServiceAccountOrganizationRoleAdmin,
	}

	_, diags := mapServiceAccountsListEntry(sa)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
}

func TestServiceAccountsStringOrNull(t *testing.T) {
	if v := serviceAccountsStringOrNull(""); !v.IsNull() {
		t.Errorf("expected null for empty string, got %v", v)
	}
	if v := serviceAccountsStringOrNull("x"); v.ValueString() != "x" {
		t.Errorf("expected value 'x', got %v", v)
	}
}
