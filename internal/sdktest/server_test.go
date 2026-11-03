// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package sdktest

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"syscall"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// recorderTB stands in for the test a Server reports to, so a test can check
// that an assertion fails when it should. Everything but Errorf goes to the
// real test.
type recorderTB struct {
	testing.TB
	mu   sync.Mutex
	errs []string
}

func newRecorderTB(t *testing.T) *recorderTB {
	t.Helper()
	return &recorderTB{TB: t}
}

func (r *recorderTB) Helper() {}

func (r *recorderTB) Errorf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}

// failures returns the errors reported so far and forgets them.
func (r *recorderTB) failures() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	errs := r.errs
	r.errs = nil
	return errs
}

// expectFailure fails t unless rec reported exactly one error containing
// want.
func expectFailure(t *testing.T, rec *recorderTB, want string) {
	t.Helper()
	errs := rec.failures()
	if len(errs) != 1 || !strings.Contains(errs[0], want) {
		t.Errorf("reported %q, want one error containing %q", errs, want)
	}
}

func expectNoFailure(t *testing.T, rec *recorderTB) {
	t.Helper()
	if errs := rec.failures(); len(errs) != 0 {
		t.Errorf("reported %q, want no error", errs)
	}
}

func TestServerRoutesRecordsAndAsserts(t *testing.T) {
	rec := newRecorderTB(t)
	s := NewServer(rec)
	s.Handle("POST /v1/things/{id}", func(w http.ResponseWriter, r *http.Request) {
		WriteJSON(w, http.StatusOK, map[string]any{"id": r.PathValue("id")})
	})
	c := NewAPIKeyClient(t, s.Server, option.WithHeader("anthropic-beta", "b1, b2"), option.WithHeaderAdd("anthropic-beta", "b3"))

	var out map[string]any
	if err := c.Post(context.Background(), "/v1/things/t1?beta=true", map[string]any{"name": "x", "tags": nil}, &out); err != nil {
		t.Fatal(err)
	}
	if out["id"] != "t1" {
		t.Errorf("response %v", out)
	}

	reqs := s.Requests("POST /v1/things/{id}")
	if len(reqs) != 1 {
		t.Fatalf("recorded %d requests", len(reqs))
	}
	r := reqs[0]
	if r.Method != http.MethodPost || r.Path != "/v1/things/t1" || r.Query.Encode() != "beta=true" || r.Fault != "" {
		t.Errorf("recorded %+v", r)
	}
	if got := strings.Join(r.Betas(), ","); got != "b1,b2,b3" {
		t.Errorf("Betas() = %q", got)
	}
	var body map[string]any
	if err := r.DecodeJSON(&body); err != nil || body["name"] != "x" {
		t.Errorf("DecodeJSON = %v, %v", body, err)
	}

	s.AssertCalled("POST /v1/things/{id}", 1)
	s.AssertBeta("POST /v1/things/{id}", "b2")
	s.AssertCredential("POST /v1/things/{id}", APIKey)
	s.AssertQueries("POST /v1/things/{id}", "beta=true")
	s.AssertWalks("POST /v1/things/{id}", "beta=true")
	s.AssertJSONBodies("POST /v1/things/{id}", `{"tags": null, "name": "x"}`)
	expectNoFailure(t, rec)

	s.AssertCalled("POST /v1/things/{id}", 2)
	expectFailure(t, rec, "called 1 times, want 2")
	s.AssertBeta("POST /v1/things/{id}", "b4")
	expectFailure(t, rec, `want it to include "b4"`)
	s.AssertNoBeta("POST /v1/things/{id}")
	expectFailure(t, rec, "want none")
	s.AssertCredential("POST /v1/things/{id}", AuthToken)
	expectFailure(t, rec, "want auth_token only")
	s.AssertCredential("POST /v1/things/{id}", AdminKey)
	expectFailure(t, rec, "want admin_api_key only")
	s.AssertQueries("POST /v1/things/{id}", "beta=false")
	expectFailure(t, rec, `want: ["beta=false"]`)
	s.AssertWalks("POST /v1/things/{id}", "beta=true", "page=2")
	expectFailure(t, rec, "do not form complete walks")
	s.AssertWalks("POST /v1/things/{id}", "x=1")
	expectFailure(t, rec, `has query "beta=true", want "x=1"`)
	s.AssertJSONBodies("POST /v1/things/{id}", `{"name": "x"}`)
	expectFailure(t, rec, "$.tags: got null, want absent")
	s.AssertJSONBodies("POST /v1/things/{id}")
	expectFailure(t, rec, "want 0 bodies")
	s.AssertBeta("GET /never", "b1")
	expectFailure(t, rec, "was never called")
}

func TestServerCredentials(t *testing.T) {
	rec := newRecorderTB(t)
	s := NewServer(rec)
	s.Respond("GET /v1/me", http.StatusOK, map[string]any{"ok": true})
	ctx := context.Background()
	if err := NewAuthTokenClient(t, s.Server).Get(ctx, "/v1/me", nil, nil); err != nil {
		t.Fatal(err)
	}
	s.AssertCredential("GET /v1/me", AuthToken)
	expectNoFailure(t, rec)
	s.AssertCredential("GET /v1/me", APIKey)
	expectFailure(t, rec, `sent x-api-key="" Authorization="Bearer sk-ant-oat01-sdktest"`)
	s.AssertCredential("GET /v1/me", Credential(9))
	expectFailure(t, rec, "unknown credential Credential(9)")
	if got := Credential(9).String(); got != "Credential(9)" {
		t.Errorf("String() = %q", got)
	}
}

func TestServerUnexpectedRequestFailsTheTest(t *testing.T) {
	rec := newRecorderTB(t)
	s := NewServer(rec)
	err := NewAPIKeyClient(t, s.Server, option.WithMaxRetries(0)).Get(context.Background(), "/v1/nowhere?x=1", nil, nil)
	var apiErr *anthropic.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound {
		t.Fatalf("err = %v, want a 404 *anthropic.Error", err)
	}
	expectFailure(t, rec, "unexpected request GET /v1/nowhere?x=1: no route matches")
	if r := s.Requests(""); len(r) != 1 || r[0].Route != "" {
		t.Errorf("recorded %+v", r)
	}
}

// TestWriteAPIErrorIsTheSDKErrorShape checks the envelope against the SDK's
// own parsing: status, type, message and request ID all come through.
func TestWriteAPIErrorIsTheSDKErrorShape(t *testing.T) {
	s := NewServer(t)
	s.Handle("GET /v1/x", func(w http.ResponseWriter, _ *http.Request) {
		WriteAPIError(w, http.StatusConflict, "", "already there")
	})
	err := NewAPIKeyClient(t, s.Server, option.WithMaxRetries(0)).Get(context.Background(), "/v1/x", nil, nil)
	var apiErr *anthropic.Error
	if !errors.As(err, &apiErr) {
		t.Fatalf("err = %v", err)
	}
	if apiErr.StatusCode != http.StatusConflict || apiErr.Type() != "conflict_error" ||
		!strings.Contains(apiErr.RawJSON(), `"message":"already there"`) || !strings.HasPrefix(apiErr.RequestID, "req_sdktest_") {
		t.Errorf("parsed %d %q %s %q", apiErr.StatusCode, apiErr.Type(), apiErr.RawJSON(), apiErr.RequestID)
	}
}

func TestErrorType(t *testing.T) {
	for status, want := range map[int]string{
		400: "invalid_request_error", 401: "authentication_error", 402: "billing_error", 403: "permission_error",
		404: "not_found_error", 409: "conflict_error", 413: "request_too_large", 429: "rate_limit_error",
		500: "api_error", 504: "timeout_error", 529: "overloaded_error", 418: "invalid_request_error", 503: "api_error",
	} {
		if got := ErrorType(status); got != want {
			t.Errorf("ErrorType(%d) = %q, want %q", status, got, want)
		}
	}
}

func TestFailNextAndResetNext(t *testing.T) {
	s := NewServer(t)
	s.Respond("/v1/x", http.StatusOK, `{"ok":true}`)
	s.FailNext("/v1/x", http.StatusServiceUnavailable, 2)
	s.ResetNext("/v1/x", 1)
	c := NewAPIKeyClient(t, s.Server, option.WithMaxRetries(0))
	ctx := context.Background()

	for i := range 2 {
		var apiErr *anthropic.Error
		if err := c.Get(ctx, "/v1/x", nil, nil); !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusServiceUnavailable || apiErr.Type() != "api_error" {
			t.Errorf("request %d: err = %v, want the injected 503", i+1, err)
		}
	}
	// A POST, which net/http does not replay on its own.
	if err := c.Post(ctx, "/v1/x", map[string]any{}, nil); err == nil || !errors.Is(err, syscall.ECONNRESET) {
		t.Errorf("request 3: err = %v, want a connection reset", err)
	}
	var out map[string]any
	if err := c.Get(ctx, "/v1/x", nil, &out); err != nil || out["ok"] != true {
		t.Errorf("request 4: %v, %v", out, err)
	}
	var faults []string
	for _, r := range s.Requests("/v1/x") {
		faults = append(faults, r.Fault)
	}
	if got := strings.Join(faults, ","); got != "status 503,status 503,reset," {
		t.Errorf("faults %q", got)
	}
}

func TestSDKRetriesAreVisible(t *testing.T) {
	// The default client retries a 503 twice: three requests for one call.
	s := NewServer(t)
	s.Respond("GET /v1/x", http.StatusOK, nil)
	s.FailNext("GET /v1/x", http.StatusServiceUnavailable, 2)
	if err := NewAPIKeyClient(t, s.Server).Get(context.Background(), "/v1/x", nil, nil); err != nil {
		t.Fatal(err)
	}
	s.AssertCalled("GET /v1/x", 3)
}

func TestMaxRequests(t *testing.T) {
	rec := newRecorderTB(t)
	s := NewServer(rec)
	s.SetMaxRequests(1)
	s.Respond("GET /v1/x", http.StatusOK, nil)
	c := NewAPIKeyClient(t, s.Server, option.WithMaxRetries(0))
	if err := c.Get(context.Background(), "/v1/x", nil, nil); err != nil {
		t.Fatal(err)
	}
	if err := c.Get(context.Background(), "/v1/x", nil, nil); err == nil {
		t.Error("request beyond the budget succeeded")
	}
	expectFailure(t, rec, "more than 1 requests")
}

func TestStatusAndSequenceServers(t *testing.T) {
	ctx := context.Background()
	for _, status := range TerminalReadStatuses {
		s := NewStatusServer(t, status)
		var apiErr *anthropic.Error
		err := NewAuthTokenClient(t, s.Server).Get(ctx, "/v1/anything", nil, nil)
		if !errors.As(err, &apiErr) || apiErr.StatusCode != status {
			t.Errorf("status server %d: err = %v", status, err)
		}
		s.AssertCalled("/", 1)
	}
	if err := NewAPIKeyClient(t, NewStatusServer(t, http.StatusNoContent).Server).Get(ctx, "/v1/x", nil, nil); err != nil {
		t.Errorf("204 server: %v", err)
	}

	rec := newRecorderTB(t)
	s := NewSequenceServer(rec,
		Response{Body: `{"n":1}`, Header: http.Header{"X-Extra": {"yes"}}},
		Response{Status: http.StatusCreated, Body: []byte(`{"n":2}`)},
		Response{Body: map[string]any{"n": 3}},
		Response{Status: http.StatusNotFound},
	)
	c := NewAPIKeyClient(t, s.Server, option.WithMaxRetries(0))
	for want := 1; want <= 3; want++ {
		var out struct{ N int }
		var raw *http.Response
		if err := c.Get(ctx, "/v1/x", nil, &out, option.WithResponseInto(&raw)); err != nil || out.N != want {
			t.Errorf("response %d: %+v, %v", want, out, err)
		}
		if want == 1 && raw.Header.Get("X-Extra") != "yes" {
			t.Error("header not sent")
		}
	}
	var apiErr *anthropic.Error
	if err := c.Get(ctx, "/v1/x", nil, nil); !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("response 4: %v", err)
	}
	expectNoFailure(t, rec)
	_ = c.Get(ctx, "/v1/x", nil, nil)
	expectFailure(t, rec, "beyond the 4 canned responses")
}

func TestParts(t *testing.T) {
	s := NewServer(t)
	var live []Part
	s.Handle("POST /v1/upload", func(w http.ResponseWriter, r *http.Request) {
		var err error
		if live, err = ReadParts(r); err != nil {
			t.Error(err)
		}
		WriteJSON(w, http.StatusOK, map[string]any{})
	})
	body := "--B\r\nContent-Disposition: form-data; name=\"files[]\"; filename=\"dir/SKILL.md\"\r\n\r\nhello\r\n" +
		"--B\r\nContent-Disposition: form-data; name=\"title\"\r\n\r\nT\r\n--B--\r\n"
	req, err := http.NewRequest(http.MethodPost, s.URL+"/v1/upload", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "multipart/form-data; boundary=B")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()

	recorded, err := s.Requests("POST /v1/upload")[0].Parts()
	if err != nil {
		t.Fatal(err)
	}
	want := []Part{{Field: "files[]", Filename: "dir/SKILL.md", Content: []byte("hello")}, {Field: "title", Content: []byte("T")}}
	for _, got := range [][]Part{live, recorded} {
		if fmt.Sprintf("%q", got) != fmt.Sprintf("%q", want) {
			t.Errorf("parts %q, want %q (the directory must survive)", got, want)
		}
	}
	if _, err := (Request{Header: http.Header{"Content-Type": {"application/json"}}}).Parts(); err == nil {
		t.Error("Parts accepted a JSON body")
	}
	if _, err := (Request{}).Parts(); err == nil {
		t.Error("Parts accepted a body without content type")
	}
}

func TestWriteJSONRefusesNonJSON(t *testing.T) {
	s := NewServer(t)
	s.Respond("GET /v1/x", http.StatusOK, map[string]any{"c": make(chan int)})
	err := NewAPIKeyClient(t, s.Server, option.WithMaxRetries(0)).Get(context.Background(), "/v1/x", nil, nil)
	var apiErr *anthropic.Error
	if !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusInternalServerError || !strings.Contains(apiErr.RawJSON(), "response is not JSON") {
		t.Errorf("err = %v, want a 500 naming the encoding error", err)
	}
}
