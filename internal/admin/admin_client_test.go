// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package admin

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// --- APIError ---

func TestAdminAPIError_Error(t *testing.T) {
	withType := &APIError{StatusCode: 404, ErrType: "not_found", Message: "workspace not found"}
	if got := withType.Error(); got != "API error (404 not_found): workspace not found" {
		t.Fatalf("unexpected: %q", got)
	}

	noType := &APIError{StatusCode: 500, Message: "internal server error"}
	if got := noType.Error(); got != "API error (500): internal server error" {
		t.Fatalf("unexpected: %q", got)
	}
}

// --- IsNotFound ---

func TestIsNotFound(t *testing.T) {
	if !IsNotFound(&APIError{StatusCode: 404, Message: "not found"}) {
		t.Fatal("expected true for 404 APIError")
	}
	if IsNotFound(&APIError{StatusCode: 500, Message: "error"}) {
		t.Fatal("expected false for 500 APIError")
	}
	if IsNotFound(errors.New("random error")) {
		t.Fatal("expected false for non-APIError")
	}
}

// --- doRequest ---

func newTestAdminClient(t *testing.T, srv *httptest.Server) *Client {
	t.Helper()
	return &Client{
		ApiKey:     "test-key",
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
	}
}

func TestAdminClient_doRequest_sendsCorrectHeaders(t *testing.T) {
	var gotAPIKey, gotVersion, gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAPIKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		gotContentType = r.Header.Get("content-type")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	client := newTestAdminClient(t, srv)
	_, err := client.DoRequest(context.Background(), "POST", "/v1/organizations/workspaces", map[string]string{"name": "x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotAPIKey != "test-key" {
		t.Errorf("x-api-key = %q, want %q", gotAPIKey, "test-key")
	}
	if gotVersion != AnthropicVersion {
		t.Errorf("anthropic-version = %q, want %q", gotVersion, AnthropicVersion)
	}
	if gotContentType != "application/json" {
		t.Errorf("content-type = %q, want %q", gotContentType, "application/json")
	}
}

func TestAdminClient_doRequest_noContentTypeWithoutBody(t *testing.T) {
	var gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotContentType = r.Header.Get("content-type")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	client := newTestAdminClient(t, srv)
	_, err := client.DoRequest(context.Background(), "GET", "/v1/organizations/workspaces/ws_1", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotContentType != "" {
		t.Errorf("expected no content-type for GET, got %q", gotContentType)
	}
}

func TestAdminClient_doRequest_jsonErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"type":"not_found_error","message":"workspace not found"}}`)
	}))
	defer srv.Close()

	client := newTestAdminClient(t, srv)
	_, err := client.DoRequest(context.Background(), "GET", "/v1/organizations/workspaces/missing", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !IsNotFound(err) {
		t.Errorf("expected IsNotFound true, got false; err = %v", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.ErrType != "not_found_error" {
		t.Errorf("ErrType = %q, want %q", apiErr.ErrType, "not_found_error")
	}
	if apiErr.Message != "workspace not found" {
		t.Errorf("Message = %q, want %q", apiErr.Message, "workspace not found")
	}
}

func TestAdminClient_doRequest_nonJsonErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `Internal Server Error`)
	}))
	defer srv.Close()

	client := newTestAdminClient(t, srv)
	_, err := client.DoRequest(context.Background(), "GET", "/v1/organizations/workspaces/ws_1", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.StatusCode != 500 {
		t.Errorf("StatusCode = %d, want 500", apiErr.StatusCode)
	}
	if !strings.Contains(apiErr.Message, "Internal Server Error") {
		t.Errorf("Message = %q, want to contain 'Internal Server Error'", apiErr.Message)
	}
}

func TestAdminClient_doRequest_successReturnsBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"id":"ws_abc123","name":"test"}`)
	}))
	defer srv.Close()

	client := newTestAdminClient(t, srv)
	body, err := client.DoRequest(context.Background(), "GET", "/v1/organizations/workspaces/ws_abc123", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var result map[string]string
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("parse response: %v", err)
	}
	if result["id"] != "ws_abc123" {
		t.Errorf("id = %q, want %q", result["id"], "ws_abc123")
	}
}

// --- retry ---

// newRetryingTestClient returns a client with retries enabled and backoff
// delays short enough to keep the suite fast.
func newRetryingTestClient(t *testing.T, srv *httptest.Server, maxRetries int) *Client {
	t.Helper()
	c := newTestAdminClient(t, srv)
	c.MaxRetries = maxRetries
	c.BaseRetryDelay = time.Millisecond
	c.MaxRetryDelay = 4 * time.Millisecond
	return c
}

func TestAdminClient_doRequest_retriesTransientStatuses(t *testing.T) {
	for _, status := range []int{
		http.StatusRequestTimeout,
		http.StatusConflict,
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == 1 {
					w.WriteHeader(status)
					return
				}
				w.WriteHeader(http.StatusOK)
				_, _ = io.WriteString(w, `{"id":"ws_ok"}`)
			}))
			defer srv.Close()

			body, err := newRetryingTestClient(t, srv, 2).
				DoRequest(context.Background(), "GET", "/v1/organizations/workspaces", nil)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if calls != 2 {
				t.Errorf("calls = %d, want 2", calls)
			}
			if !strings.Contains(string(body), "ws_ok") {
				t.Errorf("body = %q, want the second response", body)
			}
		})
	}
}

func TestAdminClient_doRequest_doesNotRetryClientErrors(t *testing.T) {
	for _, status := range []int{
		http.StatusBadRequest,
		http.StatusUnauthorized,
		http.StatusForbidden,
		http.StatusNotFound,
	} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var calls int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(status)
			}))
			defer srv.Close()

			_, err := newRetryingTestClient(t, srv, 2).
				DoRequest(context.Background(), "GET", "/v1/organizations/workspaces", nil)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if calls != 1 {
				t.Errorf("calls = %d, want 1 (no retry)", calls)
			}
		})
	}
}

func TestAdminClient_doRequest_zeroValueClientDoesNotRetry(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	// newTestAdminClient leaves MaxRetries at its zero value, as admintest.NewClient does.
	_, err := newTestAdminClient(t, srv).
		DoRequest(context.Background(), "GET", "/v1/organizations/workspaces", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if calls != 1 {
		t.Errorf("calls = %d, want 1 (retries disabled by default)", calls)
	}
}

func TestAdminClient_doRequest_exhaustsRetriesAndReturnsLastError(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = io.WriteString(w, `{"error":{"type":"overloaded_error","message":"overloaded"}}`)
	}))
	defer srv.Close()

	_, err := newRetryingTestClient(t, srv, 2).
		DoRequest(context.Background(), "GET", "/v1/organizations/workspaces", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if calls != 3 {
		t.Errorf("calls = %d, want 3 (initial attempt + 2 retries)", calls)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.ErrType != "overloaded_error" {
		t.Errorf("ErrType = %q, want %q", apiErr.ErrType, "overloaded_error")
	}
}

func TestAdminClient_doRequest_honoursXShouldRetryHeader(t *testing.T) {
	t.Run("true forces a retry on a non-retryable status", func(t *testing.T) {
		var calls int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			if calls == 1 {
				w.Header().Set("x-should-retry", "true")
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{}`)
		}))
		defer srv.Close()

		if _, err := newRetryingTestClient(t, srv, 2).
			DoRequest(context.Background(), "GET", "/v1/organizations/workspaces", nil); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if calls != 2 {
			t.Errorf("calls = %d, want 2", calls)
		}
	})

	t.Run("false suppresses a retry on a retryable status", func(t *testing.T) {
		var calls int
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls++
			w.Header().Set("x-should-retry", "false")
			w.WriteHeader(http.StatusInternalServerError)
		}))
		defer srv.Close()

		if _, err := newRetryingTestClient(t, srv, 2).
			DoRequest(context.Background(), "GET", "/v1/organizations/workspaces", nil); err == nil {
			t.Fatal("expected error, got nil")
		}
		if calls != 1 {
			t.Errorf("calls = %d, want 1", calls)
		}
	})
}

func TestAdminClient_doRequest_replaysBodyOnRetry(t *testing.T) {
	var bodies []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		if len(bodies) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{}`)
	}))
	defer srv.Close()

	_, err := newRetryingTestClient(t, srv, 2).
		DoRequest(context.Background(), "POST", "/v1/organizations/workspaces", map[string]string{"name": "x"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(bodies) != 2 {
		t.Fatalf("got %d requests, want 2", len(bodies))
	}
	if bodies[0] != bodies[1] {
		t.Errorf("retried body = %q, want the original %q", bodies[1], bodies[0])
	}
	if !strings.Contains(bodies[1], `"name":"x"`) {
		t.Errorf("retried body = %q, want it to carry the payload", bodies[1])
	}
}

func TestAdminClient_doRequest_retriesConnectionErrors(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	url := srv.URL
	srv.Close() // nothing is listening, so every attempt is a transport error

	client := &Client{
		ApiKey:         "test-key",
		BaseURL:        url,
		HTTPClient:     &http.Client{Timeout: time.Second},
		MaxRetries:     2,
		BaseRetryDelay: time.Millisecond,
		MaxRetryDelay:  4 * time.Millisecond,
	}
	_, err := client.DoRequest(context.Background(), "GET", "/v1/organizations/workspaces", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var apiErr *APIError
	if errors.As(err, &apiErr) {
		t.Fatalf("expected a transport error, got *APIError: %v", err)
	}
}

func TestAdminClient_doRequest_stopsWhenContextIsCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := newTestAdminClient(t, srv)
	client.MaxRetries = 100
	client.BaseRetryDelay = 50 * time.Millisecond
	client.MaxRetryDelay = 50 * time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	_, err := client.DoRequest(ctx, "GET", "/v1/organizations/workspaces", nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// --- retryDelay ---

func TestClient_retryDelay_exponentialBackoffWithJitter(t *testing.T) {
	c := &Client{BaseRetryDelay: time.Second, MaxRetryDelay: 8 * time.Second}

	for attempt, want := range map[int]time.Duration{0: time.Second, 1: 2 * time.Second, 2: 4 * time.Second} {
		got := c.retryDelay(nil, attempt)
		// Jitter shaves off up to a quarter of the delay.
		if got > want || got < want-want/4 {
			t.Errorf("attempt %d: delay = %v, want between %v and %v", attempt, got, want-want/4, want)
		}
	}
}

func TestClient_retryDelay_capsAtMaxRetryDelay(t *testing.T) {
	c := &Client{BaseRetryDelay: time.Second, MaxRetryDelay: 4 * time.Second}
	for _, attempt := range []int{3, 10, 100} {
		if got := c.retryDelay(nil, attempt); got > 4*time.Second {
			t.Errorf("attempt %d: delay = %v, want at most 4s", attempt, got)
		}
	}
}

func TestClient_retryDelay_zeroValuesFallBackToDefaults(t *testing.T) {
	c := &Client{}
	got := c.retryDelay(nil, 0)
	if got > DefaultBaseRetryDelay || got < DefaultBaseRetryDelay-DefaultBaseRetryDelay/4 {
		t.Errorf("delay = %v, want close to %v", got, DefaultBaseRetryDelay)
	}
}

func TestClient_retryDelay_honoursRetryAfterHeaders(t *testing.T) {
	c := &Client{BaseRetryDelay: time.Hour, MaxRetryDelay: time.Hour}

	tests := []struct {
		name    string
		headers map[string]string
		want    time.Duration
	}{
		{"retry-after-ms", map[string]string{"retry-after-ms": "1500"}, 1500 * time.Millisecond},
		{"retry-after seconds", map[string]string{"retry-after": "3"}, 3 * time.Second},
		{"retry-after fractional seconds", map[string]string{"retry-after": "0.5"}, 500 * time.Millisecond},
		{"retry-after-ms wins over retry-after", map[string]string{"retry-after-ms": "250", "retry-after": "30"}, 250 * time.Millisecond},
		{"negative retry-after clamps to zero", map[string]string{"retry-after": "-5"}, 0},
		{"oversized retry-after clamps to the ceiling", map[string]string{"retry-after": "86400"}, maxRetryAfter},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp := &http.Response{Header: http.Header{}}
			for k, v := range tc.headers {
				resp.Header.Set(k, v)
			}
			if got := c.retryDelay(resp, 0); got != tc.want {
				t.Errorf("delay = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestClient_retryDelay_retryAfterHTTPDate(t *testing.T) {
	c := &Client{BaseRetryDelay: time.Hour, MaxRetryDelay: time.Hour}
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("retry-after", time.Now().Add(5*time.Second).UTC().Format(http.TimeFormat))

	got := c.retryDelay(resp, 0)
	if got < 3*time.Second || got > 5*time.Second {
		t.Errorf("delay = %v, want roughly 5s", got)
	}
}

func TestClient_retryDelay_ignoresUnparseableRetryAfter(t *testing.T) {
	c := &Client{BaseRetryDelay: time.Second, MaxRetryDelay: time.Second}
	resp := &http.Response{Header: http.Header{}}
	resp.Header.Set("retry-after", "not-a-duration")

	// Falls through to the exponential backoff rather than returning zero.
	if got := c.retryDelay(resp, 0); got < 750*time.Millisecond || got > time.Second {
		t.Errorf("delay = %v, want the backoff value", got)
	}
}

// --- shouldRetry ---

func TestShouldRetry_nilResponseIsAConnectionError(t *testing.T) {
	if !shouldRetry(nil) {
		t.Error("expected true for a nil response")
	}
}

// --- NewClient ---

func TestNewClient_enablesRetriesByDefault(t *testing.T) {
	c := NewClient("key")
	if c.MaxRetries != DefaultMaxRetries {
		t.Errorf("MaxRetries = %d, want %d", c.MaxRetries, DefaultMaxRetries)
	}
}

func TestAdminClient_doRequest_doesNotRetryRequestConstructionErrors(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// A space is not a valid token in an HTTP method, so the request never leaves.
	_, err := newRetryingTestClient(t, srv, 2).
		DoRequest(context.Background(), "BAD METHOD", "/v1/organizations/workspaces", nil)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "create HTTP request") {
		t.Errorf("err = %v, want a request-construction error", err)
	}
	if calls != 0 {
		t.Errorf("calls = %d, want 0", calls)
	}
}
