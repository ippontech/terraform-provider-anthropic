// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package userprofiles

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ippontech/terraform-provider-anthropic/internal/oauthtest"
)

func TestMapUserProfileDSToListObject(t *testing.T) {
	profile := &anthropic.BetaUserProfile{
		ID:         "uprof_01ABC",
		CreatedAt:  time.Now(),
		UpdatedAt:  time.Now(),
		Type:       anthropic.BetaUserProfileTypeUserProfile,
		AccessType: anthropic.BetaUserProfileAccessTypeApplication,
	}

	obj, diags := mapUserProfileDSToListObject(profile)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	if obj.IsNull() || obj.IsUnknown() {
		t.Fatal("expected a known, non-null object value")
	}
}

func TestBuildUserProfilesListParams_forwardsOrderWhenSet(t *testing.T) {
	params := buildUserProfilesListParams(UserProfilesDataSourceModel{Order: types.StringValue("desc")})
	if params.Order != "desc" {
		t.Errorf("order = %q, want desc", params.Order)
	}
}

func TestBuildUserProfilesListParams_omitsOrderWhenUnset(t *testing.T) {
	for name, data := range map[string]UserProfilesDataSourceModel{
		"null":    {Order: types.StringNull()},
		"unknown": {Order: types.StringUnknown()},
	} {
		t.Run(name, func(t *testing.T) {
			params := buildUserProfilesListParams(data)
			if params.Order != "" {
				t.Errorf("order = %q, want empty", params.Order)
			}
		})
	}
}

// TestUserProfilesList_paginatesAndForwardsOrder drives the exact SDK call
// Read makes against a two-page fake server: every page is fetched, "order"
// is forwarded on the first request and the second carries the opaque cursor.
func TestUserProfilesList_paginatesAndForwardsOrder(t *testing.T) {
	var gotQueries []url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQueries = append(gotQueries, r.URL.Query())
		if r.URL.Path != "/v1/user_profiles" {
			t.Errorf("path = %q, want /v1/user_profiles", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("page") == "" {
			_, _ = io.WriteString(w, `{"data":[`+userProfileJSON("uprof_01")+`],"next_page":"cursor-2"}`)
			return
		}
		_, _ = io.WriteString(w, `{"data":[`+userProfileJSON("uprof_02")+`],"next_page":""}`)
	}))
	defer srv.Close()

	params := buildUserProfilesListParams(UserProfilesDataSourceModel{Order: types.StringValue("asc")})
	pager := oauthtest.NewSDKClient(t, srv).Beta.UserProfiles.ListAutoPaging(context.Background(), params)

	var ids []string
	for pager.Next() {
		ids = append(ids, pager.Current().ID)
	}
	if err := pager.Err(); err != nil {
		t.Fatalf("unexpected pagination error: %v", err)
	}
	if len(ids) != 2 || ids[0] != "uprof_01" || ids[1] != "uprof_02" {
		t.Fatalf("expected 2 profiles across 2 pages, got %v", ids)
	}

	if len(gotQueries) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(gotQueries))
	}
	if got := gotQueries[0].Get("order"); got != "asc" {
		t.Errorf("order = %q, want asc", got)
	}
	if got := gotQueries[1].Get("page"); got != "cursor-2" {
		t.Errorf("second request page = %q, want cursor-2", got)
	}
}

func TestUserProfilesList_omitsOrderWhenUnset(t *testing.T) {
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[],"next_page":""}`)
	}))
	defer srv.Close()

	params := buildUserProfilesListParams(UserProfilesDataSourceModel{Order: types.StringNull()})
	pager := oauthtest.NewSDKClient(t, srv).Beta.UserProfiles.ListAutoPaging(context.Background(), params)

	count := 0
	for pager.Next() {
		count++
	}
	if err := pager.Err(); err != nil {
		t.Fatalf("unexpected pagination error: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 profiles, got %d", count)
	}
	if _, present := gotQuery["order"]; present {
		t.Errorf("order sent as %q, want omitted", gotQuery.Get("order"))
	}
}

func userProfileJSON(id string) string {
	return `{"type":"user_profile","id":"` + id + `","access_type":"application",` +
		`"metadata":{},"trust_grants":{},"created_at":"2026-08-01T00:00:00Z","updated_at":"2026-08-01T00:00:00Z"}`
}
