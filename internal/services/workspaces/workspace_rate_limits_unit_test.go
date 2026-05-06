// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestWorkspaceRateLimitsDataSource_paginatesTransparently(t *testing.T) {
	page1 := `{
		"data": [
			{"type":"workspace_rate_limit","group_type":"batch","models":null,"limits":[{"type":"requests_per_day","value":500,"org_limit":null}]}
		],
		"has_more": true,
		"first_id": "rl_01",
		"last_id": "rl_01"
	}`
	page2 := `{
		"data": [
			{"type":"workspace_rate_limit","group_type":"files","models":null,"limits":[{"type":"requests_per_day","value":200,"org_limit":5000}]}
		],
		"has_more": false,
		"first_id": "rl_02",
		"last_id": "rl_02"
	}`

	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("after_id") == "" {
			_, _ = io.WriteString(w, page1)
		} else {
			_, _ = io.WriteString(w, page2)
		}
	}))
	defer srv.Close()

	client := newTestAdminClient(t, srv)

	allItems := fetchAllPages(t, client, "/v1/organizations/workspaces/wrkspc_01ABC/rate_limits?limit=1000",
		func(b []byte) (pageData[workspaceRateLimitAPIItem], error) {
			var p workspaceRateLimitsPage
			if err := json.Unmarshal(b, &p); err != nil {
				return pageData[workspaceRateLimitAPIItem]{}, err
			}
			return pageData[workspaceRateLimitAPIItem]{data: p.Data, hasMore: p.HasMore, lastID: p.LastID}, nil
		})

	if callCount != 2 {
		t.Errorf("expected 2 HTTP calls, got %d", callCount)
	}
	if len(allItems) != 2 {
		t.Fatalf("expected 2 items after pagination, got %d", len(allItems))
	}
	if allItems[0].GroupType != "batch" {
		t.Errorf("first item group_type = %q, want batch", allItems[0].GroupType)
	}
	if allItems[1].GroupType != "files" {
		t.Errorf("second item group_type = %q, want files", allItems[1].GroupType)
	}
}

func TestWorkspaceRateLimitsDataSource_emptyWorkspace(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[],"has_more":false,"first_id":"","last_id":""}`)
	}))
	defer srv.Close()

	client := newTestAdminClient(t, srv)

	respBytes, err := client.DoRequest(context.Background(), "GET", "/v1/organizations/workspaces/ws_empty/rate_limits?limit=1000", nil)
	if err != nil {
		t.Fatalf("DoRequest: %v", err)
	}
	var page workspaceRateLimitsPage
	if err := json.Unmarshal(respBytes, &page); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(page.Data) != 0 {
		t.Errorf("expected 0 items, got %d", len(page.Data))
	}
	if page.HasMore {
		t.Error("expected has_more=false for empty workspace")
	}
}

func TestWorkspaceRateLimitsDataSource_parsesNullableOrgLimit(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{
			"data": [
				{
					"type": "workspace_rate_limit",
					"group_type": "model_group",
					"models": ["claude-3-5-sonnet-20241022"],
					"limits": [
						{"type": "requests_per_minute", "value": 100, "org_limit": 1000},
						{"type": "tokens_per_day", "value": 500000, "org_limit": null}
					]
				}
			],
			"has_more": false,
			"first_id": "rl_01",
			"last_id": "rl_01"
		}`)
	}))
	defer srv.Close()

	client := newTestAdminClient(t, srv)
	body, err := client.DoRequest(context.Background(), "GET",
		"/v1/organizations/workspaces/wrkspc_01ABC/rate_limits?limit=1000", nil)
	if err != nil {
		t.Fatalf("DoRequest: %v", err)
	}

	var page workspaceRateLimitsPage
	if err := json.Unmarshal(body, &page); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(page.Data) != 1 {
		t.Fatalf("expected 1 item, got %d", len(page.Data))
	}
	item := page.Data[0]
	if len(item.Limits) != 2 {
		t.Fatalf("expected 2 limits, got %d", len(item.Limits))
	}
	if item.Limits[0].OrgLimit == nil || *item.Limits[0].OrgLimit != 1000 {
		t.Errorf("limits[0].org_limit = %v, want 1000", item.Limits[0].OrgLimit)
	}
	if item.Limits[1].OrgLimit != nil {
		t.Errorf("limits[1].org_limit = %v, want nil (unset)", item.Limits[1].OrgLimit)
	}
}

func TestWorkspaceRateLimitsDataSource_groupTypeQueryParam(t *testing.T) {
	var capturedQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"data":[],"has_more":false,"first_id":"","last_id":""}`)
	}))
	defer srv.Close()

	client := newTestAdminClient(t, srv)
	_, err := client.DoRequest(context.Background(), "GET",
		"/v1/organizations/workspaces/wrkspc_01ABC/rate_limits?group_type=model_group&limit=1000", nil)
	if err != nil {
		t.Fatalf("DoRequest: %v", err)
	}
	if capturedQuery != "group_type=model_group&limit=1000" {
		t.Errorf("query = %q, want group_type=model_group&limit=1000", capturedQuery)
	}
}
