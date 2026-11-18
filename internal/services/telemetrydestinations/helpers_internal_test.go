// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package telemetrydestinations

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	"github.com/ippontech/terraform-provider-anthropic/internal/admintest"
)

var objectAsOptions = basetypes.ObjectAsOptions{}

// recordedRequest is one call the fake Admin API received.
type recordedRequest struct {
	Method  string
	Path    string
	Query   string
	Beta    string
	RawBody string
	Body    map[string]any
}

// fakeTelemetryAPI is an httptest server standing in for the telemetry
// destination endpoints. Like the real API, it answers 404 to any request
// without the beta header, so a call that forgets it fails the test.
type fakeTelemetryAPI struct {
	srv *httptest.Server

	mu       sync.Mutex
	requests []recordedRequest
}

func newFakeTelemetryAPI(t *testing.T, handle func(r recordedRequest) (int, string)) (*fakeTelemetryAPI, *admin.Client) {
	t.Helper()
	f := &fakeTelemetryAPI{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		rec := recordedRequest{
			Method: req.Method,
			Path:   req.URL.Path,
			Query:  req.URL.RawQuery,
			Beta:   req.Header.Get("anthropic-beta"),
		}
		if b, _ := io.ReadAll(req.Body); len(b) > 0 {
			rec.RawBody = string(b)
			_ = json.Unmarshal(b, &rec.Body)
		}
		f.mu.Lock()
		f.requests = append(f.requests, rec)
		f.mu.Unlock()

		w.Header().Set("content-type", "application/json")
		if rec.Beta != telemetryDestinationsBeta {
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"type":"error","error":{"type":"not_found_error","message":"Not found"}}`)
			return
		}
		status, body := handle(rec)
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(f.srv.Close)
	return f, admintest.NewClient(t, f.srv)
}

// calls returns the requests received so far, as "METHOD path".
func (f *fakeTelemetryAPI) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.requests))
	for i, r := range f.requests {
		out[i] = r.Method + " " + r.Path
	}
	return out
}

func (f *fakeTelemetryAPI) request(t *testing.T, i int) recordedRequest {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if i >= len(f.requests) {
		t.Fatalf("expected at least %d requests, got %d", i+1, len(f.requests))
	}
	return f.requests[i]
}

// assertBetaOnEveryRequest fails unless every request carried exactly the
// telemetry destinations beta header.
func (f *fakeTelemetryAPI) assertBetaOnEveryRequest(t *testing.T) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		t.Fatal("expected at least one request")
	}
	for _, r := range f.requests {
		if r.Beta != telemetryDestinationsBeta {
			t.Errorf("%s %s: anthropic-beta = %q, want %q", r.Method, r.Path, r.Beta, telemetryDestinationsBeta)
		}
	}
}

func notFound() (int, string) {
	return http.StatusNotFound, `{"type":"error","error":{"type":"not_found_error","message":"Telemetry destination not found"}}`
}

const testDestinationID = "tdst_01AAAAAAAAAAAAAAAAAAAAAA"

// destinationJSON is a destination as the API returns it; overrides replace
// top-level fields.
func destinationJSON(t *testing.T, overrides map[string]any) string {
	t.Helper()
	d := map[string]any{
		"type":         "telemetry_destination",
		"id":           testDestinationID,
		"display_name": "Collector",
		"endpoint_url": "https://gateway.example.com/otlp",
		"header_names": []string{"authorization"},
		"state":        map[string]any{"type": "enabled"},
		"traces": map[string]any{
			"content_mode":      "metadata_only",
			"enabled":           true,
			"endpoint_url":      nil,
			"max_content_bytes": nil,
		},
		"workspaces": map[string]any{"type": "include"},
		"delivery_health": map[string]any{
			"last_error":      nil,
			"last_error_at":   nil,
			"last_success_at": "2026-10-20T19:05:00Z",
		},
		"created_at": "2026-10-20T19:00:00Z",
		"updated_at": "2026-10-20T19:00:00Z",
	}
	for k, v := range overrides {
		d[k] = v
	}
	b, err := json.Marshal(d)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return string(b)
}

// workspacesPageJSON is one page of a destination's workspace list.
func workspacesPageJSON(t *testing.T, nextPage *string, ids ...string) string {
	t.Helper()
	data := make([]map[string]any, len(ids))
	for i, id := range ids {
		data[i] = map[string]any{
			"type":                     "telemetry_destination_workspace",
			"telemetry_destination_id": testDestinationID,
			"workspace_id":             id,
			"created_at":               "2026-10-20T19:00:00Z",
		}
	}
	b, err := json.Marshal(map[string]any{"data": data, "next_page": nextPage})
	if err != nil {
		t.Fatalf("marshal page: %v", err)
	}
	return string(b)
}
