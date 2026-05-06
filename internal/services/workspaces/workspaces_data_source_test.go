// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func workspacesListFixture(id, name string, archivedAt *string) workspaceAPIResponse {
	return workspaceAPIResponse{
		ID:           id,
		Name:         name,
		ArchivedAt:   archivedAt,
		CreatedAt:    "2026-01-01T00:00:00Z",
		DisplayColor: "#AABBCC",
		Type:         "workspace",
		DataResidency: workspaceAPIDataResidency{
			AllowedInferenceGeos: json.RawMessage(`"unrestricted"`),
			DefaultInferenceGeo:  "global",
			WorkspaceGeo:         "us",
		},
	}
}

func TestWorkspacesDataSource_singlePage(t *testing.T) {
	ws1 := workspacesListFixture("wrkspc_01", "ws-one", nil)
	ws2 := workspacesListFixture("wrkspc_02", "ws-two", nil)

	page := workspaceListAPIResponse{
		Data:    []workspaceAPIResponse{ws1, ws2},
		HasMore: false,
		LastID:  nil,
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusOK)
		b, _ := json.Marshal(page)
		_, _ = w.Write(b)
	}))
	defer srv.Close()

	client := newTestAdminClient(t, srv)

	var respBytes []byte
	var err error
	respBytes, err = client.DoRequest(context.Background(), "GET", "/v1/organizations/workspaces?limit=1000", nil)
	if err != nil {
		t.Fatalf("DoRequest: %v", err)
	}

	var got workspaceListAPIResponse
	if err := json.Unmarshal(respBytes, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got.Data) != 2 {
		t.Fatalf("want 2 workspaces, got %d", len(got.Data))
	}
	if got.Data[0].ID != "wrkspc_01" {
		t.Errorf("Data[0].ID = %q, want wrkspc_01", got.Data[0].ID)
	}
	if got.HasMore {
		t.Error("HasMore should be false for single page")
	}
}

func TestWorkspacesDataSource_pagination(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusOK)

		afterID := r.URL.Query().Get("after_id")
		var page workspaceListAPIResponse
		if afterID == "" {
			lastID := "wrkspc_01"
			page = workspaceListAPIResponse{
				Data:    []workspaceAPIResponse{workspacesListFixture("wrkspc_01", "ws-one", nil)},
				HasMore: true,
				LastID:  &lastID,
			}
		} else {
			page = workspaceListAPIResponse{
				Data:    []workspaceAPIResponse{workspacesListFixture("wrkspc_02", "ws-two", nil)},
				HasMore: false,
				LastID:  nil,
			}
		}
		b, _ := json.Marshal(page)
		_, _ = w.Write(b)
	}))
	defer srv.Close()

	client := newTestAdminClient(t, srv)

	all := fetchAllPages(t, client, "/v1/organizations/workspaces?limit=1000",
		func(b []byte) (pageData[workspaceAPIResponse], error) {
			var p workspaceListAPIResponse
			if err := json.Unmarshal(b, &p); err != nil {
				return pageData[workspaceAPIResponse]{}, err
			}
			lastID := ""
			if p.LastID != nil {
				lastID = *p.LastID
			}
			return pageData[workspaceAPIResponse]{data: p.Data, hasMore: p.HasMore, lastID: lastID}, nil
		})

	if len(all) != 2 {
		t.Fatalf("want 2 total workspaces, got %d", len(all))
	}
	if callCount != 2 {
		t.Errorf("want 2 API calls for pagination, got %d", callCount)
	}
}

func TestMapWorkspaceToListObject_archivedAt(t *testing.T) {
	archived := "2026-04-01T00:00:00Z"
	ws := workspacesListFixture("wrkspc_01", "archived", &archived)

	obj, diags := mapWorkspaceToListObject(&ws)
	if diags.HasError() {
		t.Fatalf("unexpected diags: %v", diags)
	}
	if obj == nil {
		t.Fatal("expected non-nil object")
	}
}

func TestMapWorkspaceToListObject_noArchivedAt(t *testing.T) {
	ws := workspacesListFixture("wrkspc_01", "active", nil)

	obj, diags := mapWorkspaceToListObject(&ws)
	if diags.HasError() {
		t.Fatalf("unexpected diags: %v", diags)
	}
	if obj == nil {
		t.Fatal("expected non-nil object")
	}
}
