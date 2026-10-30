// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces

import (
	"net/http/httptest"
	"testing"

	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	"github.com/ippontech/terraform-provider-anthropic/internal/admintest"
)

// newTestAdminClient returns an admin.Client pointed at srv instead of the real API.
func newTestAdminClient(t *testing.T, srv *httptest.Server) *admin.Client {
	t.Helper()
	return admintest.NewClient(t, srv)
}

// workspaceFixture returns a JSON workspace API response for use in tests.
func workspaceFixture() string {
	return `{
		"id": "wrkspc_01ABC",
		"name": "test-workspace",
		"archived_at": null,
		"created_at": "2026-01-01T00:00:00Z",
		"display_color": "#FF5733",
		"type": "workspace",
		"data_residency": {
			"allowed_inference_geos": "unrestricted",
			"default_inference_geo": "global",
			"workspace_geo": "us"
		}
	}`
}
