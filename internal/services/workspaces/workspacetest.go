// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
)

// newTestAdminClient returns an admin.Client pointed at srv instead of the real API.
func newTestAdminClient(t *testing.T, srv *httptest.Server) *admin.Client {
	t.Helper()
	return &admin.Client{
		ApiKey:     "test-key",
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
	}
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

type pageData[T any] struct {
	data    []T
	hasMore bool
	lastID  string
}

// fetchAllPages drives the standard pagination loop used by all list data sources.
// unmarshal converts a raw response body into a pageData[T], normalising LastID to a plain string.
func fetchAllPages[T any](t *testing.T, client *admin.Client, basePath string, unmarshal func([]byte) (pageData[T], error)) []T {
	t.Helper()
	var all []T
	afterID := ""
	for {
		path := basePath
		if afterID != "" {
			path += "&after_id=" + afterID
		}
		b, err := client.DoRequest(context.Background(), "GET", path, nil)
		if err != nil {
			t.Fatalf("DoRequest: %v", err)
		}
		page, err := unmarshal(b)
		if err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		all = append(all, page.data...)
		if !page.hasMore {
			break
		}
		afterID = page.lastID
	}
	return all
}
