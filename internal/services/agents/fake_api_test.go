// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// fakeAgentAPI is an in-memory stand-in for the agents endpoints, for
// resource.UnitTest: the real provider talks to it through ANTHROPIC_BASE_URL,
// so Terraform itself checks the plan/apply contract ("inconsistent result
// after apply", refresh errors) that a direct method call would not.
//
// Behaviour follows the API reference:
//   - update (https://platform.claude.com/docs/en/api/beta/agents/update):
//     an omitted field is preserved; metadata is a patch where a key set to
//     null is deleted and other stored keys are kept; a stale version is a
//     409; every update bumps version;
//   - an unknown ID answers 404 not_found_error
//     (https://platform.claude.com/docs/en/api/errors).
type fakeAgentAPI struct {
	srv *httptest.Server

	mu       sync.Mutex
	objects  map[string]map[string]any
	requests []fakeAgentRequest
	lastID   int
	clock    int
	// writeNotFound makes archive answer 404 while GET still finds the
	// object: it was deleted between refresh and destroy.
	writeNotFound bool
}

type fakeAgentRequest struct {
	method string
	path   string
	body   map[string]any
	status int
}

// newFakeAgentAPI starts the fake and points the provider at it with a dummy
// standard API key. t.Setenv rules out t.Parallel for the caller.
func newFakeAgentAPI(t *testing.T) *fakeAgentAPI {
	t.Helper()
	f := &fakeAgentAPI{objects: map[string]map[string]any{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/agents", f.create)
	mux.HandleFunc("GET /v1/agents/{id}", f.get)
	mux.HandleFunc("POST /v1/agents/{id}", f.update)
	mux.HandleFunc("POST /v1/agents/{id}/archive", f.archive)
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)

	t.Setenv("ANTHROPIC_BASE_URL", f.srv.URL)
	t.Setenv("ANTHROPIC_API_KEY", "test-api-key")
	t.Setenv("ANTHROPIC_ADMIN_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	return f
}

// with runs fn under the fake's lock, to inspect or change server-side state
// between test steps.
func (f *fakeAgentAPI) with(fn func(f *fakeAgentAPI)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

// requestsTo returns the recorded requests for one method and path.
func (f *fakeAgentAPI) requestsTo(method, path string) []fakeAgentRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakeAgentRequest
	for _, r := range f.requests {
		if r.method == method && r.path == path {
			out = append(out, r)
		}
	}
	return out
}

func (f *fakeAgentAPI) create(w http.ResponseWriter, r *http.Request) {
	body := f.begin(r)
	defer f.mu.Unlock()

	f.lastID++
	now := f.now()
	obj := map[string]any{
		"id":          fmt.Sprintf("agent_%03d", f.lastID),
		"type":        "agent",
		"name":        body["name"],
		"model":       body["model"],
		"description": body["description"],
		"system":      body["system"],
		"metadata":    map[string]any{},
		"mcp_servers": []any{},
		"skills":      []any{},
		"tools":       []any{},
		"multiagent":  body["multiagent"],
		"version":     1,
		"created_at":  now,
		"updated_at":  now,
		"archived_at": nil,
	}
	if m, ok := body["metadata"].(map[string]any); ok {
		obj["metadata"] = m
	}
	f.objects[obj["id"].(string)] = obj
	f.reply(w, http.StatusOK, obj)
}

func (f *fakeAgentAPI) get(w http.ResponseWriter, r *http.Request) {
	f.begin(r)
	defer f.mu.Unlock()

	obj, ok := f.objects[r.PathValue("id")]
	if !ok {
		f.notFound(w)
		return
	}
	f.reply(w, http.StatusOK, obj)
}

func (f *fakeAgentAPI) update(w http.ResponseWriter, r *http.Request) {
	body := f.begin(r)
	defer f.mu.Unlock()

	obj, ok := f.objects[r.PathValue("id")]
	if !ok {
		f.notFound(w)
		return
	}
	if v, ok := body["version"].(float64); ok && int(v) != obj["version"].(int) {
		f.reply(w, http.StatusConflict, map[string]any{
			"type":  "error",
			"error": map[string]any{"type": "conflict_error", "message": "version mismatch"},
		})
		return
	}
	for _, field := range []string{"name", "model", "description", "system", "mcp_servers", "skills", "tools", "multiagent"} {
		if v, present := body[field]; present {
			obj[field] = v
		}
	}
	if patch, ok := body["metadata"].(map[string]any); ok {
		stored := obj["metadata"].(map[string]any)
		for k, v := range patch {
			if v == nil {
				delete(stored, k)
			} else {
				stored[k] = v
			}
		}
	}
	obj["version"] = obj["version"].(int) + 1
	obj["updated_at"] = f.now()
	f.reply(w, http.StatusOK, obj)
}

func (f *fakeAgentAPI) archive(w http.ResponseWriter, r *http.Request) {
	f.begin(r)
	defer f.mu.Unlock()

	obj, ok := f.objects[r.PathValue("id")]
	if !ok || f.writeNotFound {
		f.notFound(w)
		return
	}
	obj["archived_at"] = f.now()
	f.reply(w, http.StatusOK, obj)
}

// begin locks the fake (the handler unlocks it), records the request and
// returns its decoded JSON body (nil when empty).
func (f *fakeAgentAPI) begin(r *http.Request) map[string]any {
	var body map[string]any
	if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
		_ = json.Unmarshal(raw, &body)
	}
	f.mu.Lock()
	f.requests = append(f.requests, fakeAgentRequest{method: r.Method, path: r.URL.Path, body: body})
	return body
}

func (f *fakeAgentAPI) reply(w http.ResponseWriter, status int, v any) {
	f.requests[len(f.requests)-1].status = status
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeAgentAPI) notFound(w http.ResponseWriter) {
	f.reply(w, http.StatusNotFound, map[string]any{
		"type":  "error",
		"error": map[string]any{"type": "not_found_error", "message": "Agent not found"},
	})
}

// now returns a strictly increasing RFC 3339 timestamp, so every write moves
// updated_at forward.
func (f *fakeAgentAPI) now() string {
	f.clock++
	return time.Date(2026, 1, 1, 0, 0, f.clock, 0, time.UTC).Format(time.RFC3339)
}
