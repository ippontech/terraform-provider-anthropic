// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package organizationwebhooks

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	"github.com/ippontech/terraform-provider-anthropic/internal/admintest"
)

// recordedRequest is one call the fake Admin API received.
type recordedRequest struct {
	Method string
	Path   string
	Query  string
	Beta   string
	Body   map[string]any
}

// fakeWebhookAPI is an httptest server standing in for the organization
// webhook endpoints. Like the real API, it answers 404 to any request without
// the webhooks beta header, so a call that forgets it fails the test.
type fakeWebhookAPI struct {
	srv *httptest.Server

	mu       sync.Mutex
	requests []recordedRequest
}

// newFakeWebhookAPI starts a fake whose handle function returns the status
// and body for each request carrying the beta header.
func newFakeWebhookAPI(t *testing.T, handle func(r recordedRequest) (int, string)) (*fakeWebhookAPI, *admin.Client) {
	t.Helper()
	f := &fakeWebhookAPI{}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		rec := recordedRequest{
			Method: req.Method,
			Path:   req.URL.Path,
			Query:  req.URL.RawQuery,
			Beta:   req.Header.Get("anthropic-beta"),
		}
		if b, _ := io.ReadAll(req.Body); len(b) > 0 {
			_ = json.Unmarshal(b, &rec.Body)
		}
		f.mu.Lock()
		f.requests = append(f.requests, rec)
		f.mu.Unlock()

		w.Header().Set("content-type", "application/json")
		if rec.Beta != organizationWebhooksBeta {
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
func (f *fakeWebhookAPI) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.requests))
	for i, r := range f.requests {
		out[i] = r.Method + " " + r.Path
	}
	return out
}

// request returns the i-th request received.
func (f *fakeWebhookAPI) request(t *testing.T, i int) recordedRequest {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if i >= len(f.requests) {
		t.Fatalf("expected at least %d requests, got %d", i+1, len(f.requests))
	}
	return f.requests[i]
}

// assertBetaOnEveryRequest fails unless every request carried exactly the
// webhooks beta header.
func (f *fakeWebhookAPI) assertBetaOnEveryRequest(t *testing.T) {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.requests) == 0 {
		t.Fatal("expected at least one request")
	}
	for _, r := range f.requests {
		if r.Beta != organizationWebhooksBeta {
			t.Errorf("%s %s: anthropic-beta = %q, want %q", r.Method, r.Path, r.Beta, organizationWebhooksBeta)
		}
	}
}

func notFound() (int, string) {
	return http.StatusNotFound, `{"type":"error","error":{"type":"not_found_error","message":"Webhook endpoint not found"}}`
}

// webhookJSON is an endpoint as a read returns it; overrides replace fields.
func webhookJSON(t *testing.T, overrides map[string]any) string {
	t.Helper()
	ep := map[string]any{
		"id":              "wep_011CZkZDf8HnUq2mTxRbVn4s",
		"url":             "https://example.com/anthropic-webhooks",
		"name":            "Enforcement actions",
		"description":     "",
		"enabled_events":  []string{"user_profile.enforcement_action_opened", "user_profile.enforcement_action_resolved"},
		"state":           "enabled",
		"disabled_reason": "",
		"created_at":      "2026-10-20T19:00:00Z",
		"updated_at":      "2026-10-20T19:00:00Z",
	}
	for k, v := range overrides {
		ep[k] = v
	}
	b, err := json.Marshal(ep)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return string(b)
}
