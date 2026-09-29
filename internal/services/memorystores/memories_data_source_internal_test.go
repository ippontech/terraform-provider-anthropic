// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package memorystores

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestMapMemoryDSToListObject_withAndWithoutContent(t *testing.T) {
	// The JSON.Content validity flag is set by the unmarshaler, not by
	// assigning the Go Content field directly, so build these fixtures by
	// round-tripping through JSON.
	var full anthropic.BetaManagedAgentsMemory
	if err := full.UnmarshalJSON([]byte(`{"id":"mem_01ABC","content_sha256":"abcd","content_size_bytes":5,"created_at":"2024-01-15T10:00:00Z","memory_store_id":"memstore_01ABC","memory_version_id":"memver_01ABC","path":"/notes/foo.md","type":"memory","updated_at":"2024-01-15T10:00:00Z","content":"hello"}`)); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	fullObj, diags := mapMemoryDSToListObject(&full)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	fullAttrs := fullObj.(types.Object).Attributes()
	if fullAttrs["content"].(types.String).IsNull() {
		t.Error("expected content to be non-null when the API returns content")
	}
	if got := fullAttrs["content"].(types.String).ValueString(); got != "hello" {
		t.Errorf("content = %q, want hello", got)
	}

	var basic anthropic.BetaManagedAgentsMemory
	if err := basic.UnmarshalJSON([]byte(`{"id":"mem_02DEF","content_sha256":"efgh","content_size_bytes":0,"created_at":"2024-01-15T10:00:00Z","memory_store_id":"memstore_01ABC","memory_version_id":"memver_02DEF","path":"/notes/bar.md","type":"memory","updated_at":"2024-01-15T10:00:00Z","content":null}`)); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	basicObj, diags := mapMemoryDSToListObject(&basic)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	basicAttrs := basicObj.(types.Object).Attributes()
	if !basicAttrs["content"].(types.String).IsNull() {
		t.Errorf("expected content to be null, got %q", basicAttrs["content"].(types.String).ValueString())
	}
}

const memoryJSONTemplate = `{
	"id": "%s",
	"content_sha256": "abcd",
	"content_size_bytes": 5,
	"created_at": "2024-01-15T10:00:00Z",
	"memory_store_id": "memstore_01ABC",
	"memory_version_id": "memver_01ABC",
	"path": "%s",
	"type": "memory",
	"updated_at": "2024-01-15T10:00:00Z",
	"content": null
}`

const memoryPrefixJSONTemplate = `{
	"path": "%s",
	"type": "memory_prefix"
}`

func sprintfMemory(id, path string) string {
	return fmt.Sprintf(memoryJSONTemplate, id, path)
}

func sprintfMemoryPrefix(path string) string {
	return fmt.Sprintf(memoryPrefixJSONTemplate, path)
}

// TestMemoriesList_paginatesAndSplitsUnion exercises the SDK call the Read
// method makes against a fake two-page server, asserting that both pages are
// fetched and that memory / memory_prefix items are routed correctly.
func TestMemoriesList_paginatesAndSplitsUnion(t *testing.T) {
	var gotQueries []url.Values

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQueries = append(gotQueries, r.URL.Query())
		w.Header().Set("Content-Type", "application/json")

		if r.URL.Query().Get("page") == "" {
			_, _ = io.WriteString(w, `{"data":[`+sprintfMemory("mem_01ABC", "/notes/foo.md")+`,`+sprintfMemoryPrefix("/projects/")+`],"next_page":"cursor-2"}`)
			return
		}
		_, _ = io.WriteString(w, `{"data":[`+sprintfMemory("mem_02DEF", "/notes/bar.md")+`],"next_page":""}`)
	}))
	defer srv.Close()

	client := newTestSDKClient(t, srv)

	pager := client.Beta.MemoryStores.Memories.ListAutoPaging(t.Context(), "memstore_01ABC", anthropic.BetaMemoryStoreMemoryListParams{
		PathPrefix: param.NewOpt("/notes/"),
		Depth:      param.NewOpt(int64(1)),
	})

	var memoryIDs []string
	var prefixPaths []string
	for pager.Next() {
		item := pager.Current()
		switch item.Type {
		case "memory_prefix":
			prefixPaths = append(prefixPaths, item.AsMemoryPrefix().Path)
		default:
			memoryIDs = append(memoryIDs, item.AsMemory().ID)
		}
	}
	if err := pager.Err(); err != nil {
		t.Fatalf("unexpected pagination error: %v", err)
	}

	if len(memoryIDs) != 2 || memoryIDs[0] != "mem_01ABC" || memoryIDs[1] != "mem_02DEF" {
		t.Fatalf("expected 2 memories across 2 pages, got %v", memoryIDs)
	}
	if len(prefixPaths) != 1 || prefixPaths[0] != "/projects/" {
		t.Fatalf("expected 1 memory_prefix, got %v", prefixPaths)
	}

	if len(gotQueries) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(gotQueries))
	}
	if got := gotQueries[0].Get("path_prefix"); got != "/notes/" {
		t.Errorf("first request path_prefix = %q, want /notes/", got)
	}
	if got := gotQueries[0].Get("depth"); got != "1" {
		t.Errorf("first request depth = %q, want 1", got)
	}
	if got := gotQueries[1].Get("page"); got != "cursor-2" {
		t.Errorf("second request page = %q, want cursor-2", got)
	}
}

func TestMemoriesList_viewFullWhenIncludeContent(t *testing.T) {
	var gotQuery url.Values

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[],"next_page":""}`)
	}))
	defer srv.Close()

	client := newTestSDKClient(t, srv)

	pager := client.Beta.MemoryStores.Memories.ListAutoPaging(t.Context(), "memstore_01ABC", anthropic.BetaMemoryStoreMemoryListParams{
		View: anthropic.BetaManagedAgentsMemoryViewFull,
	})
	for pager.Next() {
	}
	if err := pager.Err(); err != nil {
		t.Fatalf("unexpected pagination error: %v", err)
	}

	if got := gotQuery.Get("view"); got != "full" {
		t.Errorf("view = %q, want full", got)
	}
}

func TestMemoriesList_defaultViewBasicOmitsPathPrefixAndDepth(t *testing.T) {
	var gotQuery url.Values

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[],"next_page":""}`)
	}))
	defer srv.Close()

	client := newTestSDKClient(t, srv)

	pager := client.Beta.MemoryStores.Memories.ListAutoPaging(t.Context(), "memstore_01ABC", anthropic.BetaMemoryStoreMemoryListParams{
		View: anthropic.BetaManagedAgentsMemoryViewBasic,
	})
	for pager.Next() {
	}
	if err := pager.Err(); err != nil {
		t.Fatalf("unexpected pagination error: %v", err)
	}

	if got := gotQuery.Get("view"); got != "basic" {
		t.Errorf("view = %q, want basic", got)
	}
	if _, present := gotQuery["path_prefix"]; present {
		t.Errorf("path_prefix sent as %q, want omitted", gotQuery.Get("path_prefix"))
	}
	if _, present := gotQuery["depth"]; present {
		t.Errorf("depth sent as %q, want omitted", gotQuery.Get("depth"))
	}
}

func TestMemoriesList_emptyList(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[],"next_page":""}`)
	}))
	defer srv.Close()

	client := newTestSDKClient(t, srv)

	pager := client.Beta.MemoryStores.Memories.ListAutoPaging(t.Context(), "memstore_01ABC", anthropic.BetaMemoryStoreMemoryListParams{})
	count := 0
	for pager.Next() {
		count++
	}
	if err := pager.Err(); err != nil {
		t.Fatalf("unexpected pagination error: %v", err)
	}
	if count != 0 {
		t.Fatalf("expected 0 items, got %d", count)
	}
}

func TestMemoriesList_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"not_found_error","message":"memory store not found"}}`)
	}))
	defer srv.Close()

	client := newTestSDKClient(t, srv)

	pager := client.Beta.MemoryStores.Memories.ListAutoPaging(t.Context(), "memstore_missing", anthropic.BetaMemoryStoreMemoryListParams{})
	for pager.Next() {
	}
	if err := pager.Err(); err == nil {
		t.Fatal("expected an error for a 404 response")
	}
}
