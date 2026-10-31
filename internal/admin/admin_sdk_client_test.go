// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package admin

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/ippontech/terraform-provider-anthropic/internal/platform"
)

// newGatewaySDKClient stands in for a gateway platform's SDK client: its own
// base URL, a credential that is not x-api-key, and the provider's options
// (the 404 guard, for instance). The SDK's retries are left at their default,
// which the admin client must override.
func newGatewaySDKClient(baseURL string, opts ...option.RequestOption) *anthropic.Client {
	c := anthropic.NewClient(append([]option.RequestOption{
		option.WithoutEnvironmentDefaults(),
		option.WithBaseURL(baseURL),
		option.WithHeader("Authorization", "Bearer gateway-token"),
	}, opts...)...)
	return &c
}

// newRetryingSDKTestClient is newRetryingTestClient over an SDK client.
func newRetryingSDKTestClient(baseURL string, maxRetries int, opts ...option.RequestOption) *Client {
	c := NewSDKClient(newGatewaySDKClient(baseURL, opts...))
	c.MaxRetries = maxRetries
	c.BaseRetryDelay = time.Millisecond
	c.MaxRetryDelay = 4 * time.Millisecond
	return c
}

// TestSDKClient_sendsTheRequestThroughTheSDKClient: method, path, query, body
// and content type reach the gateway unchanged, with the SDK client's
// credential and never an x-api-key, even with ANTHROPIC_API_KEY exported.
func TestSDKClient_sendsTheRequestThroughTheSDKClient(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-ant-api03-must-not-be-sent")
	t.Setenv("ANTHROPIC_ADMIN_API_KEY", "sk-ant-admin01-must-not-be-sent")

	var got *http.Request
	var gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Clone(context.Background())
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"wrkspc_1"}`))
	}))
	defer srv.Close()

	c := NewSDKClient(newGatewaySDKClient(srv.URL))

	body, err := c.DoRequest(context.Background(), "POST", "/v1/organizations/workspaces", map[string]string{"name": "x"})
	if err != nil {
		t.Fatalf("DoRequest: %v", err)
	}
	if string(body) != `{"id":"wrkspc_1"}` {
		t.Errorf("body = %s", body)
	}
	if got.Method != "POST" || got.URL.Path != "/v1/organizations/workspaces" {
		t.Errorf("request = %s %s", got.Method, got.URL.Path)
	}
	if gotBody != `{"name":"x"}` {
		t.Errorf("request body = %q", gotBody)
	}
	for header, want := range map[string]string{
		"Content-Type":      "application/json",
		"Anthropic-Version": AnthropicVersion,
		"Authorization":     "Bearer gateway-token",
		"X-Api-Key":         "",
	} {
		if v := got.Header.Get(header); v != want {
			t.Errorf("%s = %q, want %q", header, v, want)
		}
	}
	if n := len(got.Header.Values("Anthropic-Version")); n != 1 {
		t.Errorf("anthropic-version sent %d times, want once", n)
	}

	if _, err := c.DoRequest(context.Background(), "GET", "/v1/organizations/workspaces?limit=100&after_id=wrkspc_0", nil); err != nil {
		t.Fatalf("DoRequest: %v", err)
	}
	if got.URL.RawQuery != "limit=100&after_id=wrkspc_0" || gotBody != "" || got.Header.Get("Content-Type") != "" {
		t.Errorf("GET query = %q, body = %q, content-type = %q", got.URL.RawQuery, gotBody, got.Header.Get("Content-Type"))
	}
}

// TestSDKClient_resolvesUnderTheBaseURLPath: Google Cloud's base URLs carry a
// path (…/claudeOrganization/invoke), which the Admin API path extends.
func TestSDKClient_resolvesUnderTheBaseURLPath(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	const prefix = "/v1/projects/p/locations/global/claudeOrganization/invoke"
	c := NewSDKClient(newGatewaySDKClient(srv.URL + prefix))
	if _, err := c.DoRequest(context.Background(), "GET", "/v1/organizations/external_keys/arn%3Aaws%3Akms%3Aus-west-2%3A1%3Akey%2Fk", nil); err != nil {
		t.Fatalf("DoRequest: %v", err)
	}
	if want := prefix + "/v1/organizations/external_keys/arn%3Aaws%3Akms%3Aus-west-2%3A1%3Akey%2Fk"; gotPath != want {
		t.Errorf("path = %q, want %q", gotPath, want)
	}
}

// TestSDKClient_keepsThePostRetryPolicy: the admin client's policy, not the
// SDK's, decides retries. A POST is replayed only on 429; a 5xx or a 409 may
// mean the workspace exists already.
func TestSDKClient_keepsThePostRetryPolicy(t *testing.T) {
	tests := []struct {
		method    string
		status    int
		wantCalls int32
		wantErr   bool
	}{
		{"POST", http.StatusTooManyRequests, 2, false},
		{"POST", http.StatusInternalServerError, 1, true},
		{"POST", http.StatusBadGateway, 1, true},
		{"POST", http.StatusConflict, 1, true},
		{"GET", http.StatusInternalServerError, 2, false},
		{"GET", http.StatusTooManyRequests, 2, false},
		{"DELETE", http.StatusServiceUnavailable, 2, false},
	}
	for _, tt := range tests {
		t.Run(tt.method+" "+http.StatusText(tt.status), func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if calls.Add(1) == 1 {
					w.WriteHeader(tt.status)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"wrkspc_1"}`))
			}))
			defer srv.Close()

			_, err := newRetryingSDKTestClient(srv.URL, 2).DoRequest(
				context.Background(), tt.method, "/v1/organizations/workspaces", map[string]string{"name": "x"})
			if (err != nil) != tt.wantErr {
				t.Errorf("err = %v, want error %v", err, tt.wantErr)
			}
			if got := calls.Load(); got != tt.wantCalls {
				t.Errorf("calls = %d, want %d", got, tt.wantCalls)
			}
		})
	}
}

func TestSDKClient_doesNotReplayPostOnADroppedConnection(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		hijackAndClose(t, w, "")
	}))
	defer srv.Close()

	_, err := newRetryingSDKTestClient(srv.URL, 2).DoRequest(
		context.Background(), "POST", "/v1/organizations/workspaces", map[string]string{"name": "x"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("calls = %d, want 1 (a dropped connection leaves a POST ambiguous)", got)
	}
}

// TestSDKClient_boundsEachAttempt: a gateway that accepts the request and
// never answers fails the attempt once the per-attempt timeout expires, as
// the direct client's http.Client timeout does. The retry policy then
// applies: a GET is retried, a POST is not (it may have landed).
func TestSDKClient_boundsEachAttempt(t *testing.T) {
	if got := NewSDKClient(newGatewaySDKClient("http://127.0.0.1:9")).sdkAttemptTimeout; got != attemptTimeout {
		t.Errorf("NewSDKClient: sdkAttemptTimeout = %v, want %v", got, attemptTimeout)
	}
	for method, wantCalls := range map[string]int32{"GET": 3, "POST": 1} {
		t.Run(method, func(t *testing.T) {
			var calls atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				<-r.Context().Done()
			}))
			defer srv.Close()

			c := newRetryingSDKTestClient(srv.URL, 2)
			c.sdkAttemptTimeout = 50 * time.Millisecond
			// Without a per-attempt bound, only this deadline ends the call.
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			if _, err := c.DoRequest(ctx, method, "/v1/organizations/workspaces", nil); err == nil {
				t.Fatal("expected error, got nil")
			}
			if ctx.Err() != nil {
				t.Fatal("the call ran until the caller's deadline: no attempt was bounded")
			}
			if got := calls.Load(); got != wantCalls {
				t.Errorf("calls = %d, want %d", got, wantCalls)
			}
		})
	}
}

func TestSDKClient_honoursXShouldRetry(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("x-should-retry", "false")
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	if _, err := newRetryingSDKTestClient(srv.URL, 2).DoRequest(context.Background(), "GET", "/v1/organizations/workspaces", nil); err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("calls = %d, want 1", got)
	}
}

// TestSDKClient_mapsErrorsLikeTheDirectClient: an error status is an
// *APIError with the envelope's type and message, so admin.IsNotFound and
// every resource's error handling work unchanged.
func TestSDKClient_mapsErrorsLikeTheDirectClient(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		contentType string
		body        string
		want        APIError
	}{
		{
			name: "json envelope", status: http.StatusNotFound, contentType: "application/json",
			body: `{"type":"error","error":{"type":"not_found_error","message":"workspace not found"}}`,
			want: APIError{StatusCode: 404, ErrType: "not_found_error", Message: "workspace not found"},
		},
		{
			name: "plain body", status: http.StatusBadRequest, contentType: "text/plain",
			body: "bad request",
			want: APIError{StatusCode: 400, Message: "bad request"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", tt.contentType)
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()

			_, err := NewSDKClient(newGatewaySDKClient(srv.URL)).DoRequest(context.Background(), "GET", "/v1/organizations/workspaces/wrkspc_1", nil)
			var apiErr *APIError
			if !errors.As(err, &apiErr) {
				t.Fatalf("err = %v (%T), want *APIError", err, err)
			}
			if *apiErr != tt.want {
				t.Errorf("error = %+v, want %+v", *apiErr, tt.want)
			}
			if IsNotFound(err) != (tt.status == http.StatusNotFound) {
				t.Errorf("IsNotFound = %v", IsNotFound(err))
			}
		})
	}
}

// TestSDKClient_gateway404IsNotADeletion: with the platform guard on the SDK
// client, a gateway 404 is neither an *APIError (so IsNotFound is false and no
// resource drops its object) nor retried, although GET is idempotent.
func TestSDKClient_gateway404IsNotADeletion(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Not Found"}`))
	}))
	defer srv.Close()

	c := newRetryingSDKTestClient(srv.URL, 2, option.WithMiddleware(platform.NotFoundGuard(platform.AWS)))
	_, err := c.DoRequest(context.Background(), "GET", "/v1/organizations/workspaces/wrkspc_1", nil)

	if IsNotFound(err) {
		t.Fatal("a gateway 404 was read as an Admin API 404")
	}
	var unserved *platform.UnservedRouteError
	if !errors.As(err, &unserved) {
		t.Fatalf("err = %v (%T), want *platform.UnservedRouteError", err, err)
	}
	if got := calls.Load(); got != 1 {
		t.Errorf("calls = %d, want 1", got)
	}
}

// TestSDKClient_sendsNoWorkspaceHeader: Claude Platform on AWS's SDK client
// stamps anthropic-workspace-id on every request, and its Admin API routes do
// not require it. Like the direct client, the Admin API client sends none.
func TestSDKClient_sendsNoWorkspaceHeader(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	c := NewSDKClient(newGatewaySDKClient(srv.URL, option.WithHeader("anthropic-workspace-id", "wrkspc_default")))
	if _, err := c.DoRequest(context.Background(), "GET", "/v1/organizations/workspaces", nil); err != nil {
		t.Fatalf("DoRequest: %v", err)
	}
	if v := got.Get("anthropic-workspace-id"); v != "" {
		t.Errorf("anthropic-workspace-id = %q, want none", v)
	}
}
