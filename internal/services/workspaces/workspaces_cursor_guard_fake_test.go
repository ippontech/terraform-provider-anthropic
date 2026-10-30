// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces_test

import (
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// TestWorkspacesDataSource_repeatedCursor: a page that says has_more with
// the cursor the client just sent, or with an empty last_id, ends the read
// with an error, as admin.NextAfterID makes the other Admin lists do
// (CLAUDE.md, "Admin client retries"), instead of requesting the same page
// forever. A null last_id still ends the list silently.
func TestWorkspacesDataSource_repeatedCursor(t *testing.T) {
	t.Skip("bug: anthropic_workspaces requests the same page forever when has_more comes with the cursor just sent or an empty last_id; see fix/workspaces-list-cursor-guard")
	t.Parallel()
	for name, tc := range map[string]struct {
		lastID  string
		queries []string
	}{
		"repeated last_id": {lastID: "wrkspc_loop", queries: []string{"limit=1000", "after_id=wrkspc_loop&limit=1000"}},
		"empty last_id":    {lastID: "", queries: []string{"limit=1000"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := sdktest.NewServer(t)
			srv.SetMaxRequests(20)
			// Every page holds the same workspace and says there is more
			// after it (https://platform.claude.com/docs/en/api/organization/workspaces/list:
			// has_more, "last_id: string or null").
			srv.Handle("GET /v1/organizations/workspaces", func(w http.ResponseWriter, _ *http.Request) {
				sdktest.WriteJSON(w, http.StatusOK, map[string]any{
					"data": []any{map[string]any{
						"id": "wrkspc_loop", "name": "Loop", "type": "workspace", "archived_at": nil,
						"created_at": "2026-04-01T10:00:00Z", "display_color": "#6C5BB9",
						"data_residency": map[string]any{"allowed_inference_geos": "unrestricted", "default_inference_geo": "global", "workspace_geo": "us"},
					}},
					"has_more": true, "first_id": "wrkspc_loop", "last_id": tc.lastID,
				})
			})

			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(srv),
				Steps: []resource.TestStep{{
					Config:      providertest.ProviderConfig(sdktest.AdminKey) + `data "anthropic_workspaces" "test" {}`,
					ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+list\s+workspaces:.*has_more`),
				}},
			})
			srv.AssertQueries("GET /v1/organizations/workspaces", tc.queries...)
		})
	}
}
