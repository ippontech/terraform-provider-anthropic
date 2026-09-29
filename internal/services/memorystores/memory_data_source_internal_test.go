// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package memorystores

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// newTestSDKClient returns a bare *anthropic.Client authenticated with a
// dummy API key and pointed at srv, for unit tests that drive the SDK
// directly against a fake server.
func newTestSDKClient(t *testing.T, srv *httptest.Server) *anthropic.Client {
	t.Helper()
	c := anthropic.NewClient(
		option.WithoutEnvironmentDefaults(),
		option.WithBaseURL(srv.URL),
		option.WithAPIKey("test"),
	)
	return &c
}

func TestMapMemoryDSToState_withContent(t *testing.T) {
	createdAt := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2024, 1, 16, 11, 0, 0, 0, time.UTC)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "mem_01ABC",
			"content_sha256": "abcd",
			"content_size_bytes": 5,
			"created_at": "2024-01-15T10:00:00Z",
			"memory_store_id": "memstore_01ABC",
			"memory_version_id": "memver_01ABC",
			"path": "/notes/foo.md",
			"type": "memory",
			"updated_at": "2024-01-16T11:00:00Z",
			"content": "hello"
		}`))
	}))
	defer srv.Close()

	client := newTestSDKClient(t, srv)
	memory, err := client.Beta.MemoryStores.Memories.Get(t.Context(), "mem_01ABC", anthropic.BetaMemoryStoreMemoryGetParams{
		MemoryStoreID: "memstore_01ABC",
		View:          anthropic.BetaManagedAgentsMemoryViewFull,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var data memoryDSModel
	mapMemoryDSToState(memory, &data)

	if data.Content.IsNull() {
		t.Fatal("expected content to be non-null when the API returns content")
	}
	if data.Content.ValueString() != "hello" {
		t.Errorf("content = %q, want hello", data.Content.ValueString())
	}
	if data.Path.ValueString() != "/notes/foo.md" {
		t.Errorf("path = %q, want /notes/foo.md", data.Path.ValueString())
	}
	if data.CreatedAt.ValueString() != createdAt.Format(time.RFC3339) {
		t.Errorf("created_at = %q, want %q", data.CreatedAt.ValueString(), createdAt.Format(time.RFC3339))
	}
	if data.UpdatedAt.ValueString() != updatedAt.Format(time.RFC3339) {
		t.Errorf("updated_at = %q, want %q", data.UpdatedAt.ValueString(), updatedAt.Format(time.RFC3339))
	}
	if data.MemoryVersionID.ValueString() != "memver_01ABC" {
		t.Errorf("memory_version_id = %q, want memver_01ABC", data.MemoryVersionID.ValueString())
	}
	if data.Type.ValueString() != "memory" {
		t.Errorf("type = %q, want memory", data.Type.ValueString())
	}
}

func TestMapMemoryDSToState_nullContent(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"id": "mem_02DEF",
			"content_sha256": "efgh",
			"content_size_bytes": 0,
			"created_at": "2024-01-15T10:00:00Z",
			"memory_store_id": "memstore_01ABC",
			"memory_version_id": "memver_02DEF",
			"path": "/notes/bar.md",
			"type": "memory",
			"updated_at": "2024-01-15T10:00:00Z",
			"content": null
		}`))
	}))
	defer srv.Close()

	client := newTestSDKClient(t, srv)
	memory, err := client.Beta.MemoryStores.Memories.Get(t.Context(), "mem_02DEF", anthropic.BetaMemoryStoreMemoryGetParams{
		MemoryStoreID: "memstore_01ABC",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var data memoryDSModel
	mapMemoryDSToState(memory, &data)

	if !data.Content.IsNull() {
		t.Errorf("expected content to be null, got %q", data.Content.ValueString())
	}
}

func TestMemoryGet_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"not_found_error","message":"memory not found"}}`))
	}))
	defer srv.Close()

	client := newTestSDKClient(t, srv)
	_, err := client.Beta.MemoryStores.Memories.Get(t.Context(), "mem_missing", anthropic.BetaMemoryStoreMemoryGetParams{
		MemoryStoreID: "memstore_01ABC",
	})
	if err == nil {
		t.Fatal("expected an error for a 404 response")
	}
}
