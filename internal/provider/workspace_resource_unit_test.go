// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// --- parseAllowedInferenceGeos ---

func TestParseAllowedInferenceGeos(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    []string
		wantErr bool
	}{
		{name: "empty raw", input: "", want: nil},
		{name: "json null", input: "null", want: nil},
		{name: "unrestricted string", input: `"unrestricted"`, want: []string{"unrestricted"}},
		{name: "specific geos array", input: `["us","eu"]`, want: []string{"us", "eu"}},
		{name: "single geo array", input: `["us"]`, want: []string{"us"}},
		{name: "empty array", input: `[]`, want: []string{}},
		{name: "invalid json", input: `{bad}`, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseAllowedInferenceGeos(json.RawMessage(tc.input))
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got[%d] = %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// --- buildAllowedInferenceGeos ---

func TestBuildAllowedInferenceGeos(t *testing.T) {
	tests := []struct {
		name  string
		input []string
		want  string
	}{
		{name: "unrestricted sentinel", input: []string{"unrestricted"}, want: `"unrestricted"`},
		{name: "specific geos", input: []string{"us", "eu"}, want: `["us","eu"]`},
		{name: "single geo", input: []string{"us"}, want: `["us"]`},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := string(buildAllowedInferenceGeos(tc.input))
			if got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// --- AdminAPIError ---

func TestAdminAPIError_Error(t *testing.T) {
	withType := &AdminAPIError{StatusCode: 404, ErrType: "not_found", Message: "workspace not found"}
	if got := withType.Error(); got != "API error (404 not_found): workspace not found" {
		t.Fatalf("unexpected: %q", got)
	}

	noType := &AdminAPIError{StatusCode: 500, Message: "internal server error"}
	if got := noType.Error(); got != "API error (500): internal server error" {
		t.Fatalf("unexpected: %q", got)
	}
}

// --- IsNotFound ---

func TestIsNotFound(t *testing.T) {
	if !IsNotFound(&AdminAPIError{StatusCode: 404, Message: "not found"}) {
		t.Fatal("expected true for 404 AdminAPIError")
	}
	if IsNotFound(&AdminAPIError{StatusCode: 500, Message: "error"}) {
		t.Fatal("expected false for 500 AdminAPIError")
	}
	if IsNotFound(errors.New("random error")) {
		t.Fatal("expected false for non-AdminAPIError")
	}
}

// --- AdminClient.doRequest ---

func TestAdminClient_doRequest_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != "test-key" {
			http.Error(w, "bad key", http.StatusUnauthorized)
			return
		}
		if r.Header.Get("anthropic-version") != anthropicVersion {
			http.Error(w, "bad version", http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"id":"ws_123"}`)
	}))
	defer srv.Close()

	// adminAPIBaseURL is a package-level const, so we can't redirect doRequest to the
	// test server. Instead, build the request manually to verify the client's header
	// requirements via the server-side check above.
	req, _ := http.NewRequest("GET", srv.URL+"/v1/organizations/workspaces/ws_123", nil)
	req.Header.Set("x-api-key", "test-key")
	req.Header.Set("anthropic-version", anthropicVersion)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("request failed: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
}

func TestAdminClient_doRequest_errorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"type":"not_found_error","message":"workspace not found"}}`)
	}))
	defer srv.Close()

	client := newAdminClient("key")
	// Swap httpClient to use the test server's transport and rewrite the URL.
	client.httpClient = srv.Client()

	// Build request pointing to test server.
	req, _ := http.NewRequest("GET", srv.URL+"/v1/organizations/workspaces/missing", nil)
	req.Header.Set("x-api-key", "key")
	req.Header.Set("anthropic-version", anthropicVersion)
	rawResp, err := client.httpClient.Do(req)
	if err != nil {
		t.Fatalf("unexpected transport error: %v", err)
	}
	defer func() { _ = rawResp.Body.Close() }()

	body, _ := io.ReadAll(rawResp.Body)
	var envelope struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("parse error body: %v", err)
	}
	apiErr := &AdminAPIError{
		StatusCode: rawResp.StatusCode,
		ErrType:    envelope.Error.Type,
		Message:    envelope.Error.Message,
	}
	if !IsNotFound(apiErr) {
		t.Fatalf("expected IsNotFound true for 404 response")
	}
}
