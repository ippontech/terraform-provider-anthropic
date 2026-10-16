// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package claudecode

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

const (
	testRoutineID = "trig_01ABCDEFGHJKLMNOPQRSTUVW"
	testToken     = "sk-ant-oat01-test-token"
)

func TestFireRoutine_sendsRequest(t *testing.T) {
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		if want := "/v1/claude_code/routines/" + testRoutineID + "/fire"; r.URL.Path != want {
			t.Errorf("path = %s, want %s", r.URL.Path, want)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+testToken {
			t.Errorf("Authorization = %q, want the routine token as a bearer", got)
		}
		if got := r.Header.Get("anthropic-version"); got != "2023-06-01" {
			t.Errorf("anthropic-version = %q, want 2023-06-01", got)
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", got)
		}
		// The endpoint takes the routine token only; an API key must never
		// travel along.
		if got := r.Header.Get("x-api-key"); got != "" {
			t.Errorf("x-api-key = %q, want none", got)
		}
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &gotBody); err != nil {
			t.Errorf("body is not JSON: %s", raw)
		}
		writeFireSuccess(w)
	}))
	defer srv.Close()

	fired, err := fireRoutine(context.Background(), srv.Client(), srv.URL+"/", testRoutineID, testToken, "Sentry alert SEN-4521")
	if err != nil {
		t.Fatalf("fireRoutine: %v", err)
	}
	if fired.SessionID != "session_01HJKLMNOPQRSTUVWXYZ" {
		t.Errorf("session ID = %q", fired.SessionID)
	}
	if fired.SessionURL != "https://claude.ai/code/session_01HJKLMNOPQRSTUVWXYZ" {
		t.Errorf("session URL = %q", fired.SessionURL)
	}
	if gotBody["text"] != "Sentry alert SEN-4521" {
		t.Errorf("body text = %v", gotBody["text"])
	}
}

func TestFireRoutine_omitsEmptyText(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		if string(raw) != "{}" {
			t.Errorf("body = %s, want {}", raw)
		}
		writeFireSuccess(w)
	}))
	defer srv.Close()

	if _, err := fireRoutine(context.Background(), srv.Client(), srv.URL, testRoutineID, testToken, ""); err != nil {
		t.Fatalf("fireRoutine: %v", err)
	}
}

func TestFireRoutine_neverRetries(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		var calls atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			w.WriteHeader(status)
		}))

		_, err := fireRoutine(context.Background(), srv.Client(), srv.URL, testRoutineID, testToken, "")
		srv.Close()

		if err == nil {
			t.Errorf("status %d: want an error", status)
		}
		if got := calls.Load(); got != 1 {
			t.Errorf("status %d: %d requests, want exactly 1 (a retry could start the routine twice)", status, got)
		}
	}
}

func TestFireRoutine_decodesErrorEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "1800")
		w.Header().Set("request-id", "req_01abc")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"rate_limit_error","message":"Hourly fire limit reached"}}`))
	}))
	defer srv.Close()

	_, err := fireRoutine(context.Background(), srv.Client(), srv.URL, testRoutineID, testToken, "")

	var apiErr *routineFireAPIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error = %v, want a *routineFireAPIError", err)
	}
	if apiErr.StatusCode != http.StatusTooManyRequests || apiErr.Type != "rate_limit_error" ||
		apiErr.Message != "Hourly fire limit reached" || apiErr.RetryAfter != "1800" || apiErr.RequestID != "req_01abc" {
		t.Errorf("decoded error = %+v", apiErr)
	}

	detail := fireErrorDetail(testRoutineID, err)
	for _, want := range []string{testRoutineID, "rate_limit_error", "hourly fire limit", "Retry-After: 1800", "req_01abc"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail %q does not mention %q", detail, want)
		}
	}
}

func TestFireRoutine_nonJSONErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("<html>Bad Gateway</html>"))
	}))
	defer srv.Close()

	_, err := fireRoutine(context.Background(), srv.Client(), srv.URL, testRoutineID, testToken, "")

	var apiErr *routineFireAPIError
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusBadGateway {
		t.Fatalf("error = %v, want HTTP 502", err)
	}
	if detail := fireErrorDetail(testRoutineID, err); !strings.Contains(detail, "may or may not have started") {
		t.Errorf("detail %q should warn that the routine may have started", detail)
	}
}

func TestFireRoutine_rejectsUnexpectedResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"type":"something_else","session":"x"}`))
	}))
	defer srv.Close()

	_, err := fireRoutine(context.Background(), srv.Client(), srv.URL, testRoutineID, testToken, "")
	if err == nil || !strings.Contains(err.Error(), "response shape may have changed") {
		t.Fatalf("error = %v, want an unexpected-response error", err)
	}
}

func TestFireErrorDetail_statusHints(t *testing.T) {
	cases := map[int]string{
		http.StatusBadRequest:   "not paused",
		http.StatusUnauthorized: "does not belong to this routine",
		http.StatusForbidden:    "no access to routines",
		http.StatusNotFound:     "No routine has this ID",
	}
	for status, want := range cases {
		detail := fireErrorDetail(testRoutineID, &routineFireAPIError{StatusCode: status})
		if !strings.Contains(detail, want) {
			t.Errorf("status %d: detail %q does not contain %q", status, detail, want)
		}
		if strings.Contains(detail, testToken) {
			t.Errorf("status %d: detail leaks the token", status)
		}
	}
}

func TestResolveBaseURL(t *testing.T) {
	t.Setenv("ANTHROPIC_BASE_URL", "")
	if got := (&RoutineFireAction{}).resolveBaseURL(); got != defaultBaseURL {
		t.Errorf("empty ANTHROPIC_BASE_URL: base URL = %q, want %q", got, defaultBaseURL)
	}

	t.Setenv("ANTHROPIC_BASE_URL", "http://localhost:1234")
	if got := (&RoutineFireAction{}).resolveBaseURL(); got != "http://localhost:1234" {
		t.Errorf("base URL = %q, want ANTHROPIC_BASE_URL", got)
	}

	if got := (&RoutineFireAction{baseURL: "http://override"}).resolveBaseURL(); got != "http://override" {
		t.Errorf("base URL = %q, want the field override", got)
	}
}

func TestRoutineIDPattern(t *testing.T) {
	for id, want := range map[string]bool{
		testRoutineID:          true,
		"trig_abc123":          true,
		"routine_01ABC":        false,
		"trig_":                false,
		"trig_01/../../admin":  false,
		" trig_01ABCDEFGHJKLM": false,
	} {
		if got := routineIDPattern.MatchString(id); got != want {
			t.Errorf("routineIDPattern.MatchString(%q) = %v, want %v", id, got, want)
		}
	}
}

func writeFireSuccess(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"type":"routine_fire","claude_code_session_id":"session_01HJKLMNOPQRSTUVWXYZ","claude_code_session_url":"https://claude.ai/code/session_01HJKLMNOPQRSTUVWXYZ"}`))
}
