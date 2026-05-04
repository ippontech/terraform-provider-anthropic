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
