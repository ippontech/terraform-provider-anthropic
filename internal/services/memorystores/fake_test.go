// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package memorystores_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// Routes of the memory store endpoints (beta agent-memory-2026-07-22,
// ?beta=true, standard key). References, under
// https://platform.claude.com/docs/en/api/beta/memory_stores/: create,
// retrieve, update, list, delete, archive; memories/create, retrieve,
// update, list, delete.
const (
	routeCreateStore   = "POST /v1/memory_stores"
	routeListStores    = "GET /v1/memory_stores"
	routeGetStore      = "GET /v1/memory_stores/{memory_store_id}"
	routeUpdateStore   = "POST /v1/memory_stores/{memory_store_id}"
	routeDeleteStore   = "DELETE /v1/memory_stores/{memory_store_id}"
	routeArchiveStore  = "POST /v1/memory_stores/{memory_store_id}/archive"
	routeCreateMemory  = "POST /v1/memory_stores/{memory_store_id}/memories"
	routeListMemories  = "GET /v1/memory_stores/{memory_store_id}/memories"
	routeGetMemory     = "GET /v1/memory_stores/{memory_store_id}/memories/{memory_id}"
	routeUpdateMemory  = "POST /v1/memory_stores/{memory_store_id}/memories/{memory_id}"
	routeDeleteMemory  = "DELETE /v1/memory_stores/{memory_store_id}/memories/{memory_id}"
	memoryBeta         = "agent-memory-2026-07-22"
	preconditionFailed = "memory_precondition_failed_error"
)

var allRoutes = []string{
	routeCreateStore, routeListStores, routeGetStore, routeUpdateStore, routeDeleteStore, routeArchiveStore,
	routeCreateMemory, routeListMemories, routeGetMemory, routeUpdateMemory, routeDeleteMemory,
}

// memoryFake is the memory store API on three sdktest collections. Every
// memory write adds a version, as "Every change to a memory adds one
// version" (memory_versions/retrieve), whose ID the memory carries as
// memory_version_id; deleting a store deletes its memories and versions
// (delete).
type memoryFake struct {
	*sdktest.Server
	stores   *sdktest.Collection
	memories *sdktest.Collection
	versions *sdktest.Collection
	race     atomic.Pointer[string]
}

func newMemoryFake(t *testing.T, pageSize int) *memoryFake {
	t.Helper()
	srv := sdktest.NewServer(t)
	f := &memoryFake{Server: srv, stores: srv.NewCollection("memstore_"), memories: srv.NewCollection("mem_"), versions: srv.NewCollection("memver_")}
	f.Handle(routeCreateStore, f.createStore)
	f.Handle(routeListStores, func(w http.ResponseWriter, r *http.Request) {
		archived := r.URL.Query().Get("include_archived") == "true"
		f.paginate(w, r, pageSize, f.stores.List(func(s map[string]any) bool { return archived || s["archived_at"] == nil }))
	})
	f.stores.HandleGet(routeGetStore)
	f.Handle(routeUpdateStore, f.updateStore)
	f.Handle(routeDeleteStore, f.deleteStore)
	f.stores.HandleArchive(routeArchiveStore)
	f.Handle(routeCreateMemory, f.createMemory)
	f.Handle(routeListMemories, func(w http.ResponseWriter, r *http.Request) { f.listMemories(w, r, pageSize) })
	f.Handle(routeGetMemory, f.getMemory)
	f.Handle(routeUpdateMemory, f.updateMemory)
	f.Handle(routeDeleteMemory, f.deleteMemory)
	return f
}

// newStore stores a live store: description "" and metadata {} when unset
// ("Empty string when unset", create reference).
func (f *memoryFake) newStore(name, description string, metadata map[string]any) map[string]any {
	if metadata == nil {
		metadata = map[string]any{}
	}
	return f.stores.Create(map[string]any{
		"type": "memory_store", "name": name, "description": description, "metadata": metadata, "archived_at": nil,
	})
}

func (f *memoryFake) createStore(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeBody(w, r)
	if !ok {
		return
	}
	description, _ := body["description"].(string)
	metadata, _ := body["metadata"].(map[string]any)
	sdktest.WriteJSON(w, http.StatusOK, f.newStore(fmt.Sprint(body["name"]), description, metadata))
}

// liveStore answers 404 for an unknown store and 400 for an archived one,
// which accepts no write to itself or its memories (archived_at, create
// reference).
func (f *memoryFake) liveStore(w http.ResponseWriter, id string) bool {
	s, ok := f.stores.Get(id)
	switch {
	case !ok:
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "memory store "+id+" not found")
	case s["archived_at"] != nil:
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", "memory store "+id+" is archived")
	}
	return ok && s["archived_at"] == nil
}

// updateStore: name and description replace, metadata is a patch where null
// removes a key (update reference).
func (f *memoryFake) updateStore(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("memory_store_id")
	if !f.liveStore(w, id) {
		return
	}
	body, ok := decodeBody(w, r)
	if !ok {
		return
	}
	for k, v := range body {
		if v == nil {
			delete(body, k) // null leaves name and description unchanged
		}
	}
	s, _ := f.stores.Update(id, body, sdktest.Patch)
	sdktest.WriteJSON(w, http.StatusOK, s)
}

func (f *memoryFake) deleteStore(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("memory_store_id")
	if !f.stores.Delete(id) {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "memory store "+id+" not found")
		return
	}
	for _, c := range []*sdktest.Collection{f.memories, f.versions} {
		for _, o := range c.List(func(o map[string]any) bool { return o["memory_store_id"] == id }) {
			c.Delete(o["id"].(string))
		}
	}
	sdktest.WriteJSON(w, http.StatusOK, map[string]any{"type": "memory_store_deleted", "id": id})
}

func sha(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

// addVersion records one write of memory m (created, modified or deleted:
// no content on the last one) and returns its ID.
func (f *memoryFake) addVersion(m map[string]any, operation string) string {
	v := map[string]any{
		"type": "memory_version", "memory_id": m["id"], "memory_store_id": m["memory_store_id"], "operation": operation,
		"path": m["path"], "content": m["content"],
	}
	if operation == "deleted" {
		v["content"] = nil
	}
	return f.versions.Create(v)["id"].(string)
}

// withContent sets content and its hash and size on m.
func withContent(m map[string]any, content string) {
	m["content"], m["content_sha256"], m["content_size_bytes"] = content, sha(content), len(content)
}

// view renders a memory as the view query asks: content is null with basic,
// the default of every call but retrieve (view, create reference).
func view(r *http.Request, o map[string]any, fallback string) map[string]any {
	if v := r.URL.Query().Get("view"); v == "basic" || (v == "" && fallback == "basic") {
		o["content"] = nil
	}
	return o
}

// pathTaken reports whether another memory of the store has path: paths are
// unique within a store (memories/create reference), a 409 conflict_error
// otherwise (errors reference).
func (f *memoryFake) pathTaken(w http.ResponseWriter, storeID, memoryID, path string) bool {
	taken := len(f.memories.List(func(m map[string]any) bool {
		return m["memory_store_id"] == storeID && m["path"] == path && m["id"] != memoryID
	})) > 0
	if taken {
		sdktest.WriteAPIError(w, http.StatusConflict, "", "a memory already exists at "+path)
	}
	return taken
}

func (f *memoryFake) createMemory(w http.ResponseWriter, r *http.Request) {
	storeID := r.PathValue("memory_store_id")
	if !f.liveStore(w, storeID) {
		return
	}
	body, ok := decodeBody(w, r)
	if !ok || f.pathTaken(w, storeID, "", fmt.Sprint(body["path"])) {
		return
	}
	m := f.newMemory(storeID, fmt.Sprint(body["path"]), fmt.Sprint(body["content"]))
	sdktest.WriteJSON(w, http.StatusOK, view(r, m, "basic"))
}

// newMemory stores a memory and its created version.
func (f *memoryFake) newMemory(storeID, path, content string) map[string]any {
	m := map[string]any{"type": "memory", "memory_store_id": storeID, "path": path}
	withContent(m, content)
	m = f.memories.Create(m)
	m["memory_version_id"] = f.addVersion(m, "created")
	f.memories.Put(m)
	return m
}

// memory answers the memory a request names, found only under its store, or
// a 404.
func (f *memoryFake) memory(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	m, ok := f.memories.Get(r.PathValue("memory_id"))
	if !ok || m["memory_store_id"] != r.PathValue("memory_store_id") {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "memory "+r.PathValue("memory_id")+" not found")
		return nil, false
	}
	return m, true
}

func (f *memoryFake) getMemory(w http.ResponseWriter, r *http.Request) {
	if m, ok := f.memory(w, r); ok {
		sdktest.WriteJSON(w, http.StatusOK, view(r, m, "full"))
	}
}

// raceNextUpdate makes another writer set the content of the memory the
// next update names just before that update lands, after Terraform's
// refresh read it.
func (f *memoryFake) raceNextUpdate(content string) { f.race.Store(&content) }

// updateMemory applies content and path when given; a content_sha256
// precondition that does not match fails with a 409 (update reference),
// typed memory_precondition_failed_error as the provider expects.
func (f *memoryFake) updateMemory(w http.ResponseWriter, r *http.Request) {
	m, ok := f.memory(w, r)
	if !ok || !f.liveStore(w, r.PathValue("memory_store_id")) {
		return
	}
	if c := f.race.Swap(nil); c != nil {
		patch := map[string]any{}
		withContent(patch, *c)
		m, _ = f.memories.Update(m["id"].(string), patch, sdktest.Replace)
		m["memory_version_id"] = f.addVersion(m, "modified")
		f.memories.Put(m)
	}
	var body struct {
		Content      *string `json:"content"`
		Path         *string `json:"path"`
		Precondition *struct {
			ContentSha256 string `json:"content_sha256"`
		} `json:"precondition"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", err.Error())
		return
	}
	if body.Precondition != nil && body.Precondition.ContentSha256 != m["content_sha256"] {
		sdktest.WriteAPIError(w, http.StatusConflict, preconditionFailed, "content_sha256 precondition failed")
		return
	}
	patch := map[string]any{}
	if body.Path != nil {
		if f.pathTaken(w, m["memory_store_id"].(string), m["id"].(string), *body.Path) {
			return
		}
		patch["path"] = *body.Path
	}
	if body.Content != nil {
		withContent(patch, *body.Content)
	}
	m, _ = f.memories.Update(m["id"].(string), patch, sdktest.Replace)
	m["memory_version_id"] = f.addVersion(m, "modified")
	f.memories.Put(m)
	sdktest.WriteJSON(w, http.StatusOK, view(r, m, "basic"))
}

// deleteMemory keeps the memory's versions and adds a deleted one
// (memories/delete reference).
func (f *memoryFake) deleteMemory(w http.ResponseWriter, r *http.Request) {
	m, ok := f.memory(w, r)
	if !ok {
		return
	}
	f.memories.Delete(m["id"].(string))
	f.addVersion(m, "deleted")
	sdktest.WriteJSON(w, http.StatusOK, map[string]any{"type": "memory_deleted", "id": m["id"]})
}

// listMemories filters on path_prefix (whole segments) and, with depth=1,
// rolls deeper memories up into memory_prefix items. The fake's pager needs
// an id on every item, so a prefix carries one, which the API's does not.
func (f *memoryFake) listMemories(w http.ResponseWriter, r *http.Request, pageSize int) {
	storeID := r.PathValue("memory_store_id")
	if !f.stores.Has(storeID) {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "memory store "+storeID+" not found")
		return
	}
	q := r.URL.Query()
	prefix := q.Get("path_prefix")
	if prefix == "" {
		prefix = "/"
	}
	var items []map[string]any
	var rolled []string
	for _, m := range f.memories.List(func(m map[string]any) bool { return m["memory_store_id"] == storeID }) {
		rest, ok := strings.CutPrefix(m["path"].(string), prefix)
		if !ok {
			continue
		}
		if dir, _, deeper := strings.Cut(rest, "/"); deeper && q.Get("depth") == "1" {
			if p := prefix + dir + "/"; !slices.Contains(rolled, p) {
				rolled = append(rolled, p)
				items = append(items, map[string]any{"type": "memory_prefix", "path": p, "id": "prefix:" + p})
			}
			continue
		}
		items = append(items, view(r, m, "basic"))
	}
	f.paginate(w, r, pageSize, items)
}

// paginate pages a list with page/next_page, newest first for stores
// (list reference); view=full caps a page at 20 items (memories/list).
func (f *memoryFake) paginate(w http.ResponseWriter, r *http.Request, pageSize int, items []map[string]any) {
	if r.Pattern == routeListStores {
		slices.Reverse(items)
	}
	if r.URL.Query().Get("view") == "full" && (pageSize == 0 || pageSize > 20) {
		pageSize = 20
	}
	f.Paginate(w, r, sdktest.PageToken, pageSize, items)
}

// assertRequestShape checks what every request of a test must carry: the
// memory beta, the standard key and ?beta=true, on every route called.
func (f *memoryFake) assertRequestShape(t *testing.T) {
	t.Helper()
	for _, route := range allRoutes {
		if len(f.Requests(route)) == 0 {
			continue
		}
		f.AssertBeta(route, memoryBeta)
		f.AssertCredential(route, sdktest.APIKey)
		for _, r := range f.Requests(route) {
			if r.Query.Get("beta") != "true" {
				t.Errorf("%s %s without ?beta=true", r.Method, r.Path)
			}
		}
	}
}

// decodeBody reads a JSON object body or answers 400 invalid_request_error.
func decodeBody(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	body := map[string]any{}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", "body is not a JSON object: "+err.Error())
		return nil, false
	}
	return body, true
}
