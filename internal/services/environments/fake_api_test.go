// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package environments_test

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

// fakeEnvironmentAPI is an in-memory stand-in for the environments endpoints,
// for resource.UnitTest: the real provider talks to it through
// ANTHROPIC_BASE_URL, so Terraform itself checks the plan/apply contract
// ("inconsistent result after apply", refresh errors) that a direct method
// call would not.
//
// Behaviour follows the API reference:
//   - update (https://platform.claude.com/docs/en/api/beta/environments/update):
//     an omitted field is preserved; description null clears it; a metadata
//     key set to null or "" is deleted, other keys are kept;
//   - an unknown ID answers 404 not_found_error
//     (https://platform.claude.com/docs/en/api/errors).
type fakeEnvironmentAPI struct {
	srv *httptest.Server

	mu       sync.Mutex
	objects  map[string]map[string]any
	requests []fakeEnvironmentRequest
	lastID   int
	clock    int
	// writeNotFound makes DELETE and archive answer 404 while GET still
	// finds the object: it was deleted between refresh and destroy.
	writeNotFound bool
}

type fakeEnvironmentRequest struct {
	method string
	path   string
	body   map[string]any
	status int
}

// newFakeEnvironmentAPI starts the fake and points the provider at it with a
// dummy standard API key. t.Setenv rules out t.Parallel for the caller.
func newFakeEnvironmentAPI(t *testing.T) *fakeEnvironmentAPI {
	t.Helper()
	f := &fakeEnvironmentAPI{objects: map[string]map[string]any{}}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/environments", f.create)
	mux.HandleFunc("GET /v1/environments/{id}", f.get)
	mux.HandleFunc("POST /v1/environments/{id}", f.update)
	mux.HandleFunc("DELETE /v1/environments/{id}", f.delete)
	mux.HandleFunc("POST /v1/environments/{id}/archive", f.archive)
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
func (f *fakeEnvironmentAPI) with(fn func(f *fakeEnvironmentAPI)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

// requestsTo returns the recorded requests for one method and path.
func (f *fakeEnvironmentAPI) requestsTo(method, path string) []fakeEnvironmentRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []fakeEnvironmentRequest
	for _, r := range f.requests {
		if r.method == method && r.path == path {
			out = append(out, r)
		}
	}
	return out
}

func (f *fakeEnvironmentAPI) create(w http.ResponseWriter, r *http.Request) {
	body := f.begin(r)
	defer f.mu.Unlock()

	f.lastID++
	now := f.now()
	metadata := map[string]any{}
	if m, ok := body["metadata"].(map[string]any); ok {
		metadata = m
	}
	config, ok := body["config"]
	if !ok {
		config = map[string]any{
			"type":       "cloud",
			"networking": map[string]any{"type": "unrestricted"},
			"packages": map[string]any{
				"type": "packages", "apt": []any{}, "cargo": []any{}, "gem": []any{},
				"go": []any{}, "npm": []any{}, "pip": []any{},
			},
		}
	}
	obj := map[string]any{
		"id":          fmt.Sprintf("env_%03d", f.lastID),
		"type":        "environment",
		"name":        body["name"],
		"description": body["description"],
		"metadata":    metadata,
		"config":      config,
		"created_at":  now,
		"updated_at":  now,
		"archived_at": nil,
	}
	f.objects[obj["id"].(string)] = obj
	f.reply(w, http.StatusOK, obj)
}

func (f *fakeEnvironmentAPI) get(w http.ResponseWriter, r *http.Request) {
	f.begin(r)
	defer f.mu.Unlock()

	obj, ok := f.objects[r.PathValue("id")]
	if !ok {
		f.notFound(w)
		return
	}
	f.reply(w, http.StatusOK, obj)
}

func (f *fakeEnvironmentAPI) update(w http.ResponseWriter, r *http.Request) {
	body := f.begin(r)
	defer f.mu.Unlock()

	obj, ok := f.objects[r.PathValue("id")]
	if !ok {
		f.notFound(w)
		return
	}
	for _, field := range []string{"name", "description", "config"} {
		if v, present := body[field]; present {
			obj[field] = v
		}
	}
	if patch, ok := body["metadata"].(map[string]any); ok {
		stored := obj["metadata"].(map[string]any)
		for k, v := range patch {
			if v == nil || v == "" {
				delete(stored, k)
			} else {
				stored[k] = v
			}
		}
	}
	obj["updated_at"] = f.now()
	f.reply(w, http.StatusOK, obj)
}

func (f *fakeEnvironmentAPI) delete(w http.ResponseWriter, r *http.Request) {
	f.begin(r)
	defer f.mu.Unlock()

	id := r.PathValue("id")
	if _, ok := f.objects[id]; !ok || f.writeNotFound {
		f.notFound(w)
		return
	}
	delete(f.objects, id)
	f.reply(w, http.StatusOK, map[string]any{"id": id, "type": "environment_deleted"})
}

func (f *fakeEnvironmentAPI) archive(w http.ResponseWriter, r *http.Request) {
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
func (f *fakeEnvironmentAPI) begin(r *http.Request) map[string]any {
	var body map[string]any
	if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
		_ = json.Unmarshal(raw, &body)
	}
	f.mu.Lock()
	f.requests = append(f.requests, fakeEnvironmentRequest{method: r.Method, path: r.URL.Path, body: body})
	return body
}

func (f *fakeEnvironmentAPI) reply(w http.ResponseWriter, status int, v any) {
	f.requests[len(f.requests)-1].status = status
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (f *fakeEnvironmentAPI) notFound(w http.ResponseWriter) {
	f.reply(w, http.StatusNotFound, map[string]any{
		"type":  "error",
		"error": map[string]any{"type": "not_found_error", "message": "Environment not found"},
	})
}

// now returns a strictly increasing RFC 3339 timestamp, so every write moves
// updated_at forward.
func (f *fakeEnvironmentAPI) now() string {
	f.clock++
	return time.Date(2026, 1, 1, 0, 0, f.clock, 0, time.UTC).Format(time.RFC3339)
}
