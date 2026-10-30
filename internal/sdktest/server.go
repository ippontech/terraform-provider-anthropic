// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package sdktest

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"
)

// DefaultMaxRequests bounds the requests one Server answers, so a client
// stuck in a loop fails the test quickly instead of running into the
// package timeout.
const DefaultMaxRequests = 500

// Server is an httptest fake of the Anthropic API. Routes use the patterns of
// net/http.ServeMux, method included: "POST /v1/environments",
// "GET /v1/environments/{id}". The query string (?beta=true) is not part of
// the route; Request.Query keeps it.
//
// A request no route matches fails the test and gets a 404 not_found_error.
type Server struct {
	*httptest.Server

	t   testing.TB
	mux *http.ServeMux

	mu          sync.Mutex
	requests    []Request
	faults      map[string][]fault
	cursors     map[string]*cursorLog
	maxRequests int
}

// Request is one request the Server received.
type Request struct {
	Method string
	// Path is the URL path, without the query string.
	Path string
	// Route is the pattern it matched, "" when none did.
	Route  string
	Query  url.Values
	Header http.Header
	Body   []byte
	// Fault is "status 503" or "reset" when FailNext or ResetNext answered
	// it instead of the route's handler.
	Fault string
}

// Betas returns the anthropic-beta values of the request, one per beta,
// whether the client sent one comma-separated header or several headers.
// Reference: https://platform.claude.com/docs/en/api/beta-headers,
// "Multiple beta features".
func (r Request) Betas() []string {
	var betas []string
	for _, v := range r.Header.Values("Anthropic-Beta") {
		for b := range strings.SplitSeq(v, ",") {
			if b = strings.TrimSpace(b); b != "" {
				betas = append(betas, b)
			}
		}
	}
	return betas
}

// DecodeJSON unmarshals the request body into v.
func (r Request) DecodeJSON(v any) error {
	return json.Unmarshal(r.Body, v)
}

// DecodeBody reads the JSON object a handler's request carries, numbers kept
// as json.Number. A body that is not a JSON object is a bug of the client: it
// fails the test and answers 400 invalid_request_error.
func (s *Server) DecodeBody(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	body := map[string]any{}
	dec := json.NewDecoder(r.Body)
	dec.UseNumber()
	if err := dec.Decode(&body); err != nil {
		s.badRequest(w, "%s %s: body is not a JSON object: %v", r.Method, r.URL.Path, err)
		return nil, false
	}
	return body, true
}

// RequireBeta wraps h so that a request whose anthropic-beta lacks beta gets
// a 404 not_found_error, as a beta endpoint answers it: "A request without it
// returns 404, exactly as if the endpoint did not exist" (e.g.
// https://platform.claude.com/docs/en/api/beta/organization/plugins/create).
func RequireBeta(beta string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !slices.Contains(Request{Header: r.Header}.Betas(), beta) {
			WriteAPIError(w, http.StatusNotFound, "", "Not found")
			return
		}
		h(w, r)
	}
}

// Part is one part of a multipart/form-data body.
type Part struct {
	// Field is the form field name ("files[]", "display_title").
	Field string
	// Filename is the filename parameter exactly as sent. mime/multipart's
	// FileName keeps only its base name, which would hide the directory a
	// skill bundle carries ("my-skill/SKILL.md").
	Filename string
	Content  []byte
}

// Parts parses a multipart/form-data body, as the skill and file uploads
// send.
func (r Request) Parts() ([]Part, error) {
	return parseParts(r.Header.Get("Content-Type"), bytes.NewReader(r.Body))
}

// ReadParts parses the multipart/form-data body of a request a handler
// receives, as Request.Parts does.
func ReadParts(r *http.Request) ([]Part, error) {
	return parseParts(r.Header.Get("Content-Type"), r.Body)
}

func parseParts(contentType string, body io.Reader) ([]Part, error) {
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil {
		return nil, err
	}
	if mediaType != "multipart/form-data" {
		return nil, fmt.Errorf("content type %q is not multipart/form-data", mediaType)
	}
	mr := multipart.NewReader(body, params["boundary"])
	var parts []Part
	for {
		p, err := mr.NextRawPart()
		if errors.Is(err, io.EOF) {
			return parts, nil
		}
		if err != nil {
			return nil, err
		}
		_, disp, err := mime.ParseMediaType(p.Header.Get("Content-Disposition"))
		if err != nil {
			return nil, err
		}
		content, err := io.ReadAll(p)
		if err != nil {
			return nil, err
		}
		parts = append(parts, Part{Field: disp["name"], Filename: disp["filename"], Content: content})
	}
}

type fault struct {
	status  int // 0 means a connection reset
	message string
}

// NewServer starts a Server with no route and closes it when the test ends.
func NewServer(tb testing.TB) *Server {
	tb.Helper()
	s := &Server{
		t:           tb,
		mux:         http.NewServeMux(),
		faults:      map[string][]fault{},
		cursors:     map[string]*cursorLog{},
		maxRequests: DefaultMaxRequests,
	}
	s.Server = httptest.NewServer(http.HandlerFunc(s.serve))
	tb.Cleanup(s.Close)
	return s
}

// Handle registers h for route. It panics, like http.ServeMux, on a
// malformed or conflicting pattern.
func (s *Server) Handle(route string, h http.HandlerFunc) {
	s.mux.HandleFunc(route, h)
}

// Respond registers a route that always answers status with body, written
// as Response does.
func (s *Server) Respond(route string, status int, body any) {
	s.Handle(route, func(w http.ResponseWriter, _ *http.Request) {
		Response{Status: status, Body: body}.write(w)
	})
}

// SetMaxRequests changes DefaultMaxRequests for this server.
func (s *Server) SetMaxRequests(n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.maxRequests = n
}

// FailNext makes the next n requests to route fail with status and the error
// envelope, before its handler runs. The requests are recorded with their
// Fault. Mind the retries: the SDK replays 408, 409, 429 and 5xx twice, and
// internal/admin replays them on idempotent requests.
func (s *Server) FailNext(route string, status, n int) {
	s.addFaults(route, fault{status: status}, n)
}

// FailNextWith is FailNext with the error message the API gives, for a test
// that checks the message reaches the user: a canned answer to a rule of the
// API the provider does not depend on (CLAUDE.md, "What a fake holds").
func (s *Server) FailNextWith(route string, status int, message string, n int) {
	s.addFaults(route, fault{status: status, message: message}, n)
}

// ResetNext drops the connection of the next n requests to route without an
// answer, as a network failure between the request and the response does.
// Mind net/http: its transport silently replays a GET, HEAD, OPTIONS or
// TRACE that met a reset on a reused keep-alive connection, so such a reset
// can reach the client as the answer to the replay; a POST or DELETE always
// surfaces the reset (to the SDK, which may retry it).
func (s *Server) ResetNext(route string, n int) {
	s.addFaults(route, fault{}, n)
}

func (s *Server) addFaults(route string, f fault, n int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for range n {
		s.faults[route] = append(s.faults[route], f)
	}
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		s.t.Errorf("sdktest: read body of %s %s: %v", r.Method, r.URL.Path, err)
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	w.Header().Set("request-id", newRequestID())

	_, route := s.mux.Handler(r)
	rec := Request{
		Method: r.Method,
		Path:   r.URL.Path,
		Route:  route,
		Query:  r.URL.Query(),
		Header: r.Header.Clone(),
		Body:   body,
	}

	s.mu.Lock()
	var f *fault
	if queue := s.faults[route]; route != "" && len(queue) > 0 {
		f = &queue[0]
		s.faults[route] = queue[1:]
		rec.Fault = "reset"
		if f.status != 0 {
			rec.Fault = fmt.Sprintf("status %d", f.status)
		}
	}
	s.requests = append(s.requests, rec)
	tooMany := len(s.requests) > s.maxRequests
	s.mu.Unlock()

	switch {
	case tooMany:
		s.t.Errorf("sdktest: more than %d requests (last: %s %s); is the client looping?", s.maxRequests, r.Method, r.URL.Path)
		WriteAPIError(w, http.StatusBadRequest, "", "sdktest: request budget exhausted")
	case route == "":
		s.t.Errorf("sdktest: unexpected request %s %s: no route matches", r.Method, r.URL.String())
		WriteAPIError(w, http.StatusNotFound, "", "sdktest: no route for "+r.Method+" "+r.URL.Path)
	case f != nil && f.status != 0:
		message := f.message
		if message == "" {
			message = fmt.Sprintf("sdktest: injected %d", f.status)
		}
		WriteAPIError(w, f.status, "", message)
	case f != nil:
		s.reset(w, r)
	default:
		s.mux.ServeHTTP(w, r)
	}
}

// reset closes the connection with a TCP RST, so the client sees
// "connection reset by peer" rather than a clean EOF.
func (s *Server) reset(w http.ResponseWriter, r *http.Request) {
	hj, ok := w.(http.Hijacker)
	if !ok {
		s.t.Errorf("sdktest: cannot reset %s %s: the connection does not support hijacking", r.Method, r.URL.Path)
		return
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		s.t.Errorf("sdktest: hijack %s %s: %v", r.Method, r.URL.Path, err)
		return
	}
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetLinger(0)
	}
	_ = conn.Close()
}

// Requests returns the requests that matched route, in arrival order, or
// every request when route is "".
func (s *Server) Requests(route string) []Request {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []Request
	for _, r := range s.requests {
		if route == "" || r.Route == route {
			out = append(out, r)
		}
	}
	return out
}

// AssertCalled fails the test unless exactly n requests matched route,
// faulted ones included. Pass 0 to assert a route was never called.
func (s *Server) AssertCalled(route string, n int) {
	s.t.Helper()
	if got := s.Requests(route); len(got) != n {
		s.t.Errorf("sdktest: %s called %d times, want %d\n%s", route, len(got), n, s.describe())
	}
}

// AssertBeta fails the test unless route was called and every request to it
// carried beta in anthropic-beta.
func (s *Server) AssertBeta(route, beta string) {
	s.t.Helper()
	reqs := s.requests0(route)
	for i, r := range reqs {
		if !slices.Contains(r.Betas(), beta) {
			s.t.Errorf("sdktest: request %d to %s sent anthropic-beta %q, want it to include %q", i+1, route, r.Betas(), beta)
		}
	}
}

// AssertNoBeta fails the test unless route was called and no request to it
// carried an anthropic-beta header, as a GA endpoint expects.
func (s *Server) AssertNoBeta(route string) {
	s.t.Helper()
	for i, r := range s.requests0(route) {
		if b := r.Betas(); len(b) != 0 {
			s.t.Errorf("sdktest: request %d to %s sent anthropic-beta %q, want none", i+1, route, b)
		}
	}
}

// AssertCredential fails the test unless route was called and every request
// to it carried exactly credential c.
func (s *Server) AssertCredential(route string, c Credential) {
	s.t.Helper()
	for i, r := range s.requests0(route) {
		if err := c.check(r.Header); err != nil {
			s.t.Errorf("sdktest: request %d to %s %v", i+1, route, err)
		}
	}
}

// Shape is what every request to the endpoints of a service carries.
type Shape struct {
	// Credential is the one credential every request carries (see
	// AssertCredential).
	Credential Credential
	// Beta is the anthropic-beta value every request carries; "" means no
	// anthropic-beta header at all, as a GA or Admin endpoint expects.
	Beta string
	// BetaQuery requires the beta=true query flag the SDK adds to the
	// requests of a beta endpoint.
	BetaQuery bool
}

// AssertShape fails the test unless at least one request matched one of
// routes (any route when none is given) and every request that did carries
// shape, faulted ones included.
func (s *Server) AssertShape(shape Shape, routes ...string) {
	s.t.Helper()
	called := map[string]bool{}
	for _, r := range s.Requests("") {
		if r.Route != "" && (len(routes) == 0 || slices.Contains(routes, r.Route)) {
			called[r.Route] = true
		}
	}
	if len(called) == 0 {
		s.t.Errorf("sdktest: none of the routes %q was called\n%s", routes, s.describe())
		return
	}
	for _, route := range slices.Sorted(maps.Keys(called)) {
		s.AssertCredential(route, shape.Credential)
		if shape.Beta == "" {
			s.AssertNoBeta(route)
		} else {
			s.AssertBeta(route, shape.Beta)
		}
		for i, r := range s.Requests(route) {
			if shape.BetaQuery && r.Query.Get("beta") != "true" {
				s.t.Errorf("sdktest: request %d to %s has query %q, want beta=true", i+1, route, r.Query.Encode())
			}
		}
	}
}

// AssertQueries fails the test unless the requests to route carried exactly
// the encoded query strings want, one per request in arrival order. Write
// each as url.Values.Encode does, keys sorted: "after_id=m2&limit=1000"
// ("beta=true" included when the SDK adds it).
func (s *Server) AssertQueries(route string, want ...string) {
	s.t.Helper()
	reqs := s.Requests(route)
	got := make([]string, len(reqs))
	for i, r := range reqs {
		got[i] = r.Query.Encode()
	}
	if !slices.Equal(got, want) {
		s.t.Errorf("sdktest: queries to %s\n got: %q\nwant: %q", route, got, want)
	}
}

// AssertWalks fails the test unless the requests to route form one or more
// complete walks, each sending exactly the query strings walk in order (see
// AssertQueries). Terraform reads a data source several times per test step,
// a number that belongs to terraform-plugin-testing; this checks every read
// paged through the whole list the same way without pinning that number.
// Use one data source per route in the configuration, or walks interleave.
func (s *Server) AssertWalks(route string, walk ...string) {
	s.t.Helper()
	reqs := s.Requests(route)
	if len(walk) == 0 || len(reqs) == 0 || len(reqs)%len(walk) != 0 {
		s.t.Errorf("sdktest: %d requests to %s do not form complete walks of %d: %q\n%s", len(reqs), route, len(walk), walk, s.describe())
		return
	}
	for i, r := range reqs {
		if got, want := r.Query.Encode(), walk[i%len(walk)]; got != want {
			s.t.Errorf("sdktest: request %d to %s (step %d of a walk) has query %q, want %q\n%s", i+1, route, i%len(walk)+1, got, want, s.describe())
			return
		}
	}
}

// AssertJSONBodies fails the test unless the requests to route carried
// exactly the JSON bodies want, one per request in arrival order. Bodies are
// compared as JSON values (key order and spacing do not matter; an absent
// key and a null do), and a mismatch lists every differing path.
func (s *Server) AssertJSONBodies(route string, want ...string) {
	s.t.Helper()
	reqs := s.Requests(route)
	if len(reqs) != len(want) {
		s.t.Errorf("sdktest: %s called %d times, want %d bodies\n%s", route, len(reqs), len(want), s.describe())
		return
	}
	for i, r := range reqs {
		if diff := JSONDiff(string(r.Body), want[i]); diff != "" { //nolint:gosec // G602: len(reqs) == len(want), checked above
			s.t.Errorf("sdktest: body of request %d to %s differs (got -> want):\n%s", i+1, route, diff)
		}
	}
}

// requests0 returns the requests to route, failing the test when there are
// none: an assertion on every request of a route that was never called would
// otherwise pass vacuously.
func (s *Server) requests0(route string) []Request {
	s.t.Helper()
	reqs := s.Requests(route)
	if len(reqs) == 0 {
		s.t.Errorf("sdktest: %s was never called\n%s", route, s.describe())
	}
	return reqs
}

// describe lists every request received, for failure messages.
func (s *Server) describe() string {
	reqs := s.Requests("")
	if len(reqs) == 0 {
		return "no request received"
	}
	var b strings.Builder
	b.WriteString("requests received:\n")
	for i, r := range reqs {
		fmt.Fprintf(&b, "  %d. %s %s", i+1, r.Method, r.Path)
		if q := r.Query.Encode(); q != "" {
			b.WriteString("?" + q)
		}
		if r.Fault != "" {
			b.WriteString(" [" + r.Fault + "]")
		}
		b.WriteString("\n")
	}
	return b.String()
}
