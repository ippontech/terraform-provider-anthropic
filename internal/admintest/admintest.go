// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package admintest provides shared test helpers for constructing an
// admin.Client that targets a local httptest server instead of the real API.
package admintest

import (
	"net/http/httptest"
	"testing"

	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
)

// NewClient returns an *admin.Client pointed at srv instead of the real API.
func NewClient(t *testing.T, srv *httptest.Server) *admin.Client {
	t.Helper()
	return &admin.Client{
		ApiKey:     "test-key",
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
	}
}
