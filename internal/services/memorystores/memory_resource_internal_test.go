// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package memorystores

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/ippontech/terraform-provider-anthropic/internal/oauthtest"
)

func TestMapMemoryToState(t *testing.T) {
	createdAt := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
	updatedAt := time.Date(2024, 1, 16, 11, 0, 0, 0, time.UTC)

	memory := &anthropic.BetaManagedAgentsMemory{
		ID:               "mem_01ABC",
		MemoryStoreID:    "memstore_01ABC",
		Path:             "/notes.md",
		Content:          "hello world",
		ContentSha256:    "abc123",
		ContentSizeBytes: 11,
		MemoryVersionID:  "memver_01ABC",
		Type:             anthropic.BetaManagedAgentsMemoryTypeMemory,
		CreatedAt:        createdAt,
		UpdatedAt:        updatedAt,
	}

	var data MemoryResourceModel
	diags := mapMemoryToState(memory, &data)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}

	if data.ID.ValueString() != "mem_01ABC" {
		t.Errorf("expected ID mem_01ABC, got %s", data.ID.ValueString())
	}
	if data.MemoryStoreID.ValueString() != "memstore_01ABC" {
		t.Errorf("expected MemoryStoreID memstore_01ABC, got %s", data.MemoryStoreID.ValueString())
	}
	if data.Path.ValueString() != "/notes.md" {
		t.Errorf("expected Path /notes.md, got %s", data.Path.ValueString())
	}
	if data.Content.ValueString() != "hello world" {
		t.Errorf("expected Content 'hello world', got %s", data.Content.ValueString())
	}
	if data.ContentSha256.ValueString() != "abc123" {
		t.Errorf("expected ContentSha256 abc123, got %s", data.ContentSha256.ValueString())
	}
	if data.ContentSizeBytes.ValueInt64() != 11 {
		t.Errorf("expected ContentSizeBytes 11, got %d", data.ContentSizeBytes.ValueInt64())
	}
	if data.MemoryVersionID.ValueString() != "memver_01ABC" {
		t.Errorf("expected MemoryVersionID memver_01ABC, got %s", data.MemoryVersionID.ValueString())
	}
	if data.Type.ValueString() != "memory" {
		t.Errorf("expected Type memory, got %s", data.Type.ValueString())
	}
	if data.CreatedAt.ValueString() != "2024-01-15T10:00:00Z" {
		t.Errorf("expected CreatedAt 2024-01-15T10:00:00Z, got %s", data.CreatedAt.ValueString())
	}
	if data.UpdatedAt.ValueString() != "2024-01-16T11:00:00Z" {
		t.Errorf("expected UpdatedAt 2024-01-16T11:00:00Z, got %s", data.UpdatedAt.ValueString())
	}
}

func TestValidateMemoryPath(t *testing.T) {
	tests := map[string]struct {
		path    string
		wantErr bool
	}{
		"valid simple path":      {path: "/notes.md"},
		"valid nested path":      {path: "/projects/foo/notes.md"},
		"valid accented (NFC)":   {path: "/caf\u00e9.md"}, // e-acute as a single NFC codepoint
		"missing leading slash":  {path: "notes.md", wantErr: true},
		"empty segment":          {path: "/projects//notes.md", wantErr: true},
		"dot segment":            {path: "/projects/./notes.md", wantErr: true},
		"dotdot segment":         {path: "/projects/../notes.md", wantErr: true},
		"root only":              {path: "/", wantErr: true},
		"control character":      {path: "/notes\x00.md", wantErr: true},
		"format character":       {path: "/notes\u200b.md", wantErr: true}, // U+200B ZERO WIDTH SPACE (Cf)
		"line separator":         {path: "/notes\u2028.md", wantErr: true},
		"paragraph separator":    {path: "/notes\u2029.md", wantErr: true},
		"non-NFC (decomposed e)": {path: "/cafe\u0301.md", wantErr: true}, // e + combining acute accent
		"too long": {
			path:    "/" + string(make([]byte, 1025)),
			wantErr: true,
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			err := validateMemoryPath(tc.path)
			if tc.wantErr && err == nil {
				t.Fatalf("expected error for path %q, got nil", tc.path)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected no error for path %q, got %v", tc.path, err)
			}
		})
	}
}

func TestParseMemoryImportID(t *testing.T) {
	tests := map[string]struct {
		id         string
		wantStore  string
		wantMemory string
		wantErr    bool
	}{
		"valid":            {id: "memstore_01ABC:mem_01XYZ", wantStore: "memstore_01ABC", wantMemory: "mem_01XYZ"},
		"missing colon":    {id: "memstore_01ABC", wantErr: true},
		"missing store":    {id: ":mem_01XYZ", wantErr: true},
		"missing memory":   {id: "memstore_01ABC:", wantErr: true},
		"extra colon kept": {id: "memstore_01ABC:mem_01XYZ:extra", wantStore: "memstore_01ABC", wantMemory: "mem_01XYZ:extra"},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			store, memory, err := parseMemoryImportID(tc.id)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for id %q, got nil", tc.id)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if store != tc.wantStore || memory != tc.wantMemory {
				t.Fatalf("got (%q, %q), want (%q, %q)", store, memory, tc.wantStore, tc.wantMemory)
			}
		})
	}
}

// TestMemoryStoreMemoryNew_SendsFullViewAndContent exercises the SDK create
// call the resource's Create method issues: verifies the path, the
// view=full query parameter, and that content is sent even when empty.
func TestMemoryStoreMemoryNew_SendsFullViewAndContent(t *testing.T) {
	var gotPath, gotQuery string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		gotPath = req.URL.Path
		gotQuery = req.URL.RawQuery
		_ = json.NewDecoder(req.Body).Decode(&gotBody)

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"id": "mem_01ABC",
			"memory_store_id": "memstore_01ABC",
			"path": "/notes.md",
			"content": "",
			"content_sha256": "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
			"content_size_bytes": 0,
			"memory_version_id": "memver_01ABC",
			"type": "memory",
			"created_at": "2024-01-15T10:00:00Z",
			"updated_at": "2024-01-15T10:00:00Z"
		}`)
	}))
	defer srv.Close()

	client := oauthtest.NewSDKClient(t, srv)
	memory, err := client.Beta.MemoryStores.Memories.New(context.Background(), "memstore_01ABC", anthropic.BetaMemoryStoreMemoryNewParams{
		Content: param.NewOpt(""),
		Path:    "/notes.md",
		View:    anthropic.BetaManagedAgentsMemoryViewFull,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if gotPath != "/v1/memory_stores/memstore_01ABC/memories" {
		t.Errorf("path = %q, want /v1/memory_stores/memstore_01ABC/memories", gotPath)
	}
	if gotQuery != "beta=true&view=full" && gotQuery != "view=full&beta=true" {
		t.Errorf("query = %q, want to contain view=full", gotQuery)
	}
	if _, ok := gotBody["content"]; !ok {
		t.Errorf("expected content to be sent in the body even when empty, got %v", gotBody)
	}
	if memory.ID != "mem_01ABC" {
		t.Errorf("ID = %q, want mem_01ABC", memory.ID)
	}
}

// TestMemoryStoreMemoryUpdate_SendsPreconditionAndOnlyChangedFields exercises
// the SDK update call the resource's Update method issues: the precondition
// must always be present, and content/path are only sent when they changed.
func TestMemoryStoreMemoryUpdate_SendsPreconditionAndOnlyChangedFields(t *testing.T) {
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		_ = json.NewDecoder(req.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{
			"id": "mem_01ABC",
			"memory_store_id": "memstore_01ABC",
			"path": "/renamed.md",
			"content": "new content",
			"content_sha256": "def456",
			"content_size_bytes": 11,
			"memory_version_id": "memver_02DEF",
			"type": "memory",
			"created_at": "2024-01-15T10:00:00Z",
			"updated_at": "2024-01-16T11:00:00Z"
		}`)
	}))
	defer srv.Close()

	client := oauthtest.NewSDKClient(t, srv)

	// Simulate an Update where only path changed (content unchanged).
	params := anthropic.BetaMemoryStoreMemoryUpdateParams{
		MemoryStoreID: "memstore_01ABC",
		View:          anthropic.BetaManagedAgentsMemoryViewFull,
		Precondition: anthropic.BetaManagedAgentsPreconditionParam{
			Type:          anthropic.BetaManagedAgentsPreconditionTypeContentSha256,
			ContentSha256: param.NewOpt("abc123"),
		},
		Path: param.NewOpt("/renamed.md"),
	}

	_, err := client.Beta.MemoryStores.Memories.Update(context.Background(), "mem_01ABC", params)
	if err != nil {
		t.Fatalf("Update: %v", err)
	}

	precondition, ok := gotBody["precondition"].(map[string]any)
	if !ok {
		t.Fatalf("expected precondition object in body, got %v", gotBody)
	}
	if precondition["content_sha256"] != "abc123" {
		t.Errorf("precondition.content_sha256 = %v, want abc123", precondition["content_sha256"])
	}
	if _, ok := gotBody["content"]; ok {
		t.Errorf("expected content to be omitted when unchanged, got %v", gotBody["content"])
	}
	if gotBody["path"] != "/renamed.md" {
		t.Errorf("path = %v, want /renamed.md", gotBody["path"])
	}
}

// TestMemoryStoreMemoryUpdate_PreconditionFailed verifies a 409 response is
// surfaced as a distinguishable error (StatusCode 409), which the resource's
// Update method maps to a non-clobbering diagnostic.
func TestMemoryStoreMemoryUpdate_PreconditionFailed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"memory_precondition_failed_error","message":"content_sha256 precondition failed"}}`)
	}))
	defer srv.Close()

	client := oauthtest.NewSDKClient(t, srv)
	_, err := client.Beta.MemoryStores.Memories.Update(context.Background(), "mem_01ABC", anthropic.BetaMemoryStoreMemoryUpdateParams{
		MemoryStoreID: "memstore_01ABC",
		Precondition: anthropic.BetaManagedAgentsPreconditionParam{
			Type:          anthropic.BetaManagedAgentsPreconditionTypeContentSha256,
			ContentSha256: param.NewOpt("stale"),
		},
	})

	var apierr *anthropic.Error
	if !errors.As(err, &apierr) {
		t.Fatalf("expected *anthropic.Error, got: %v", err)
	}
	if apierr.StatusCode != http.StatusConflict {
		t.Errorf("StatusCode = %d, want 409", apierr.StatusCode)
	}
	if string(apierr.Type()) != memoryPreconditionFailedErrorType {
		t.Errorf("Type() = %q, want %q", apierr.Type(), memoryPreconditionFailedErrorType)
	}
}

// TestMemoryStoreMemoryUpdate_OtherConflict verifies a 409 with a different
// error type (e.g. a rename colliding with an existing path) is
// distinguishable from a precondition failure via Type(), so Update's
// "modified out-of-band" message — which tells the operator to refresh and
// re-apply, a fix that cannot help here — is only used for the right error.
func TestMemoryStoreMemoryUpdate_OtherConflict(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusConflict)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"invalid_request_error","message":"a memory already exists at this path"}}`)
	}))
	defer srv.Close()

	client := oauthtest.NewSDKClient(t, srv)
	_, err := client.Beta.MemoryStores.Memories.Update(context.Background(), "mem_01ABC", anthropic.BetaMemoryStoreMemoryUpdateParams{
		MemoryStoreID: "memstore_01ABC",
		Precondition: anthropic.BetaManagedAgentsPreconditionParam{
			Type:          anthropic.BetaManagedAgentsPreconditionTypeContentSha256,
			ContentSha256: param.NewOpt("current"),
		},
	})

	var apierr *anthropic.Error
	if !errors.As(err, &apierr) {
		t.Fatalf("expected *anthropic.Error, got: %v", err)
	}
	if apierr.StatusCode != http.StatusConflict {
		t.Errorf("StatusCode = %d, want 409", apierr.StatusCode)
	}
	if string(apierr.Type()) == memoryPreconditionFailedErrorType {
		t.Errorf("Type() = %q, want something other than %q", apierr.Type(), memoryPreconditionFailedErrorType)
	}
}

// TestMemoryStoreMemoryGet_NotFound mirrors the same pattern used by
// federation rules: a 404 must be distinguishable via errors.As so Read can
// call RemoveResource.
func TestMemoryStoreMemoryGet_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"type":"error","error":{"type":"not_found_error","message":"memory not found"}}`)
	}))
	defer srv.Close()

	client := oauthtest.NewSDKClient(t, srv)
	_, err := client.Beta.MemoryStores.Memories.Get(context.Background(), "mem_missing", anthropic.BetaMemoryStoreMemoryGetParams{
		MemoryStoreID: "memstore_01ABC",
	})

	var apierr *anthropic.Error
	if !errors.As(err, &apierr) {
		t.Fatalf("expected *anthropic.Error, got: %v", err)
	}
	if apierr.StatusCode != http.StatusNotFound {
		t.Errorf("StatusCode = %d, want 404", apierr.StatusCode)
	}
}
