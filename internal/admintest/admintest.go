// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package admintest provides shared test helpers for constructing an
// admin.Client that targets a local httptest server instead of the real API.
package admintest

import (
	"encoding/json"
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

// ListPage returns the JSON body of one page of an Admin API list endpoint:
// data, has_more, first_id and last_id, the ids null when empty.
// Reference: https://platform.claude.com/docs/en/api/organization/users/list
// ("first_id: string or null", "last_id: ... Can be used as the after_id for
// the next page"). To serve several pages from a stored list, with the cursor
// checks, use sdktest.Server.Paginate with sdktest.AfterID instead.
func ListPage[T any](data []T, hasMore bool, firstID, lastID string) string {
	if data == nil {
		data = []T{}
	}
	page := struct {
		Data    []T     `json:"data"`
		HasMore bool    `json:"has_more"`
		FirstID *string `json:"first_id"`
		LastID  *string `json:"last_id"`
	}{Data: data, HasMore: hasMore, FirstID: nullable(firstID), LastID: nullable(lastID)}
	b, err := json.Marshal(page)
	if err != nil {
		panic("admintest: page is not JSON: " + err.Error())
	}
	return string(b)
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
