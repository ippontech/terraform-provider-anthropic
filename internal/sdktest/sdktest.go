// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package sdktest is the shared fake of the Anthropic API for unit tests: an
// httptest server with a route table, a request recorder, the API's error
// envelope, both pagination families, an in-memory collection and fault
// injection, plus SDK clients pointed at it.
//
// It does not import internal/provider, so tests in a service's internal
// package (package <service>) can use it; internal/providertest adds the
// Terraform side (provider factories and configuration) for tests in the
// external package (package <service>_test) that drive resource.UnitTest.
//
// Every behaviour follows a page of the API reference, cited where it is
// implemented. Two rules keep it honest:
//   - Handlers run on server goroutines, so the fake reports a broken client
//     with t.Errorf, never t.Fatal, and answers with an error the client
//     will not retry (400), so the test ends instead of looping.
//   - Payload shapes follow the wire format of the pinned SDK (v1.67.0) and,
//     where they differ, what the deployed API was measured to send
//     (CLAUDE.md), not a reference page that is ahead of both.
package sdktest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// Dummy credentials. ProviderConfig in internal/providertest and the client
// builders below send these, so AssertCredential can tell them apart.
const (
	TestAPIKey    = "sk-ant-api03-sdktest"   //nolint:gosec // G101: a dummy value only the fake sees
	TestAdminKey  = "sk-ant-admin01-sdktest" //nolint:gosec // G101: a dummy value only the fake sees
	TestAuthToken = "sk-ant-oat01-sdktest"   //nolint:gosec // G101: a dummy value only the fake sees
)

// Credential names one of the provider's three credentials (CLAUDE.md,
// "Credentials and clients").
type Credential int

const (
	// APIKey is the standard key (api_key), sent as x-api-key.
	APIKey Credential = iota + 1
	// AdminKey is the Admin API key (admin_api_key), sent as x-api-key.
	AdminKey
	// AuthToken is the org:admin OAuth bearer (auth_token), sent as
	// Authorization: Bearer.
	AuthToken
)

func (c Credential) String() string {
	switch c {
	case APIKey:
		return "api_key"
	case AdminKey:
		return "admin_api_key"
	case AuthToken:
		return "auth_token"
	}
	return fmt.Sprintf("Credential(%d)", int(c))
}

// check reports whether h carries exactly c, and nothing else: an API
// endpoint rejects a request that presents two credentials
// (TestConfigureClientsCarryExactlyOneCredential in internal/provider).
// Reference: https://platform.claude.com/docs/en/api/overview, "Authorization"
// and "x-api-key" request headers.
func (c Credential) check(h http.Header) error {
	key, bearer := h.Get("X-Api-Key"), h.Get("Authorization")
	var wantKey, wantBearer string
	switch c {
	case APIKey:
		wantKey = TestAPIKey
	case AdminKey:
		wantKey = TestAdminKey
	case AuthToken:
		wantBearer = "Bearer " + TestAuthToken
	default:
		return fmt.Errorf("unknown credential %v", c)
	}
	if key != wantKey || bearer != wantBearer {
		return fmt.Errorf("sent x-api-key=%q Authorization=%q, want %s only (x-api-key=%q Authorization=%q)",
			key, bearer, c, wantKey, wantBearer)
	}
	return nil
}

// TerminalReadStatuses are the statuses a read-after-write consistency wait
// stops on at once instead of polling to its deadline: the object or the
// access to it is gone for good (CLAUDE.md, "Read-after-write consistency
// waits").
var TerminalReadStatuses = []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound}

// NewAPIKeyClient returns an SDK client that sends TestAPIKey to srv and
// nothing from the environment. Extra options apply last.
func NewAPIKeyClient(tb testing.TB, srv *httptest.Server, opts ...option.RequestOption) *anthropic.Client {
	tb.Helper()
	return newClient(srv, option.WithAPIKey(TestAPIKey), opts)
}

// NewAuthTokenClient returns an SDK client that sends TestAuthToken as a
// bearer to srv and nothing from the environment. Extra options apply last.
func NewAuthTokenClient(tb testing.TB, srv *httptest.Server, opts ...option.RequestOption) *anthropic.Client {
	tb.Helper()
	return newClient(srv, option.WithAuthToken(TestAuthToken), opts)
}

func newClient(srv *httptest.Server, credential option.RequestOption, opts []option.RequestOption) *anthropic.Client {
	c := anthropic.NewClient(append([]option.RequestOption{
		option.WithoutEnvironmentDefaults(),
		option.WithBaseURL(srv.URL),
		credential,
	}, opts...)...)
	return &c
}

// ErrorType returns the error type the API documents for status. Other 4xx
// statuses use invalid_request_error, as the reference allows, and other
// 5xx statuses api_error.
// Reference: https://platform.claude.com/docs/en/api/errors, "HTTP errors".
func ErrorType(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "invalid_request_error"
	case http.StatusUnauthorized:
		return "authentication_error"
	case http.StatusPaymentRequired:
		return "billing_error"
	case http.StatusForbidden:
		return "permission_error"
	case http.StatusNotFound:
		return "not_found_error"
	case http.StatusConflict:
		return "conflict_error"
	case http.StatusRequestEntityTooLarge:
		return "request_too_large"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	case http.StatusGatewayTimeout:
		return "timeout_error"
	case 529:
		return "overloaded_error"
	}
	if status >= 500 {
		return "api_error"
	}
	return "invalid_request_error"
}

var requestCounter atomic.Int64

// newRequestID returns a fresh value for the request-id header.
func newRequestID() string {
	return fmt.Sprintf("req_sdktest_%06d", requestCounter.Add(1))
}

// WriteAPIError writes the error envelope of the API, the same for the
// standard, OAuth and Admin endpoints:
//
//	{"type": "error", "error": {"type": errType, "message": message}, "request_id": "req_..."}
//
// with the request-id header. An empty errType means ErrorType(status).
// Reference: https://platform.claude.com/docs/en/api/errors, "Error shapes"
// and "Request ID".
func WriteAPIError(w http.ResponseWriter, status int, errType, message string) {
	if errType == "" {
		errType = ErrorType(status)
	}
	id := w.Header().Get("request-id")
	if id == "" {
		id = newRequestID()
		w.Header().Set("request-id", id)
	}
	WriteJSON(w, status, map[string]any{
		"type":       "error",
		"error":      map[string]any{"type": errType, "message": message},
		"request_id": id,
	})
}

// WriteJSON writes v as a JSON response with status. A value JSON cannot
// encode is a bug of the test: it answers a 500 api_error naming the
// encoding error.
func WriteJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		WriteAPIError(w, http.StatusInternalServerError, "", "sdktest: response is not JSON: "+err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// Response is one canned answer of NewSequenceServer.
type Response struct {
	// Status defaults to 200.
	Status int
	// Body is written as is when it is a string or []byte, JSON-encoded
	// otherwise. A nil Body with a Status of 400 or more writes the error
	// envelope; with a 2xx Status, {}.
	Body any
	// Header is added to the response.
	Header http.Header
}

func (resp Response) write(w http.ResponseWriter) {
	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	status := resp.Status
	if status == 0 {
		status = http.StatusOK
	}
	switch body := resp.Body.(type) {
	case nil:
		if status >= 400 {
			WriteAPIError(w, status, "", http.StatusText(status))
			return
		}
		WriteJSON(w, status, map[string]any{})
	case string:
		writeRaw(w, status, []byte(body))
	case []byte:
		writeRaw(w, status, body)
	default:
		WriteJSON(w, status, body)
	}
}

func writeRaw(w http.ResponseWriter, status int, body []byte) {
	if w.Header().Get("Content-Type") == "" {
		w.Header().Set("Content-Type", "application/json")
	}
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// NewStatusServer returns a Server that answers every request with status:
// {} for a 2xx, the error envelope otherwise. Requests are recorded.
func NewStatusServer(tb testing.TB, status int) *Server {
	tb.Helper()
	s := NewServer(tb)
	s.Handle("/", func(w http.ResponseWriter, _ *http.Request) {
		Response{Status: status}.write(w)
	})
	return s
}

// NewSequenceServer returns a Server that answers the n-th request, whatever
// its route, with responses[n]. A request beyond the last response fails the
// test.
func NewSequenceServer(tb testing.TB, responses ...Response) *Server {
	tb.Helper()
	s := NewServer(tb)
	var next atomic.Int64
	s.Handle("/", func(w http.ResponseWriter, r *http.Request) {
		i := int(next.Add(1)) - 1
		if i >= len(responses) {
			s.t.Errorf("sdktest: request %d (%s %s) beyond the %d canned responses", i+1, r.Method, r.URL.Path, len(responses))
			WriteAPIError(w, http.StatusBadRequest, "", "sdktest: no canned response left")
			return
		}
		responses[i].write(w)
	})
	return s
}
