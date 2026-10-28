// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package retry

import (
	"context"
	"io"
	"math"
	"math/rand"
	"net/http"
	"net/http/httptrace"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/anthropics/anthropic-sdk-go/option"
)

const (
	// DefaultMaxRetries mirrors the SDK, which retries twice after the
	// initial attempt.
	DefaultMaxRetries = 2
	// DefaultBaseDelay is the first backoff step; each retry doubles it, up
	// to DefaultMaxDelay. Same schedule as the SDK and internal/admin.
	DefaultBaseDelay = 500 * time.Millisecond
	// DefaultMaxDelay caps the exponential backoff.
	DefaultMaxDelay = 8 * time.Second
	// MaxRetryAfter caps how long a server-supplied retry-after header can
	// hold up an apply, as in internal/admin. The SDK honours the header
	// without a bound.
	MaxRetryAfter = 60 * time.Second
)

// Policy is the provider's retry policy for SDK requests. The zero value of a
// delay field means its default.
type Policy struct {
	// MaxRetries is the number of retries after the initial attempt.
	MaxRetries int
	// BaseDelay is the first backoff step when the server sends no
	// retry-after header.
	BaseDelay time.Duration
	// MaxDelay caps the backoff. It does not bound a server-supplied
	// retry-after, which only MaxRetryAfter bounds.
	MaxDelay time.Duration
}

// DefaultPolicy is the policy every provider SDK client uses.
var DefaultPolicy = Policy{MaxRetries: DefaultMaxRetries}

// SDKOptions puts an SDK client under p: the SDK's own retries are turned
// off and Middleware(p) retries instead. The provider's newSDKClient applies
// it to every client; a test that counts requests uses it to get production
// behaviour.
func SDKOptions(p Policy) []option.RequestOption {
	return []option.RequestOption{
		option.WithMaxRetries(0),
		option.WithMiddleware(Middleware(p)),
	}
}

// Middleware retries a failed request according to the method, like
// internal/admin's DoRequest, instead of the SDK's single rule for all
// methods (2 retries on 408, 409, 429, 5xx and connection errors).
//
// Idempotent requests keep the SDK's rule: GET, DELETE and the rest of the
// RFC 9110 set, and the POSTs that leave the same state when sent twice
// (see isIdempotent). Any other request, a POST that creates an object,
// uploads a file, sends a message or updates with a precondition, is
// replayed only on 429 or 529, which report a capacity limit (rate limited,
// overloaded) rather than a failure of the request, and on a connection
// error raised before the request got a connection (DNS, dial, TLS
// handshake), when none of it can have reached the API. After
// another 5xx, a 408, a 409 or a connection lost once the request had a
// connection, the write may have landed, and a replay would create a
// duplicate (an archive-only agent, a second file, a second billed message)
// or turn an applied precondition update into a misleading 409. The API
// documents no idempotency key that would make the replay safe.
//
// An x-should-retry header from the server settles the decision either way
// for an idempotent request, as in the SDK. For any other request it can
// only turn a retry off: it says the error is transient, not that the write
// did not land. The delay honours retry-after-ms / retry-after, bounded by
// MaxRetryAfter, and otherwise backs off exponentially with jitter.
//
// Install it with SDKOptions, which turns the SDK's own retries off;
// otherwise both layers retry.
func Middleware(p Policy) option.Middleware {
	return func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		idempotent := isIdempotent(req)
		for attempt := 0; ; attempt++ {
			r, err := attemptRequest(req, attempt)
			if err != nil {
				return nil, err
			}
			r, connected := traceConnection(r)
			resp, err := next(r)
			if attempt >= p.MaxRetries || !replayable(req) || !shouldRetry(idempotent, resp, connected.Load()) {
				return resp, err
			}
			drain(resp)
			if err := sleep(req.Context(), p.delay(resp, attempt)); err != nil {
				return nil, err
			}
		}
	}
}

// attemptRequest returns the request to send for an attempt: req itself the
// first time, then a clone with a fresh body and the SDK's retry counter
// header, when present, set to the attempt number.
func attemptRequest(req *http.Request, attempt int) (*http.Request, error) {
	if attempt == 0 {
		return req, nil
	}
	r := req.Clone(req.Context())
	if req.GetBody != nil {
		body, err := req.GetBody()
		if err != nil {
			return nil, err
		}
		r.Body = body
	}
	if r.Header.Get("X-Stainless-Retry-Count") != "" {
		r.Header.Set("X-Stainless-Retry-Count", strconv.Itoa(attempt))
	}
	return r, nil
}

// traceConnection returns r with a client trace that records whether the
// request got a connection. Until it has one (DNS lookup, dial and TLS
// handshake done), no byte of it can have reached the API. The flag is
// atomic because the HTTP/2 transport calls the hook from its own goroutine.
func traceConnection(r *http.Request) (*http.Request, *atomic.Bool) {
	connected := new(atomic.Bool)
	trace := &httptrace.ClientTrace{
		GotConn: func(httptrace.GotConnInfo) { connected.Store(true) },
	}
	return r.WithContext(httptrace.WithClientTrace(r.Context(), trace)), connected
}

// drain reads and closes the body of a response that is about to be retried,
// so that its connection can be reused.
func drain(resp *http.Response) {
	if resp != nil && resp.Body != nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}
}

// sleep waits for d, or returns the context's error if it is done first.
func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// replayable reports whether the request body can be sent again. The SDK
// buffers JSON and multipart bodies alike and sets GetBody.
func replayable(req *http.Request) bool {
	return req.Body == nil || req.Body == http.NoBody || req.GetBody != nil
}

// statusOverloaded is the API's 529 overloaded_error, "The API is
// temporarily overloaded" (https://platform.claude.com/docs/en/api/errors).
// Like a 429 it reports a capacity limit, not a failure of this request,
// unlike a 500 (an unexpected internal error) or a 504 (timed out while
// processing), after which the write may have landed.
const statusOverloaded = 529

// shouldRetry decides whether a failed attempt is worth repeating. resp is
// nil after a connection error; connected reports whether the request had
// got a connection by then.
func shouldRetry(idempotent bool, resp *http.Response, connected bool) bool {
	if resp == nil {
		return idempotent || !connected
	}

	header := resp.Header.Get("x-should-retry")
	if header == "false" {
		return false
	}

	if !idempotent {
		return resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == statusOverloaded
	}

	return header == "true" ||
		resp.StatusCode == http.StatusRequestTimeout ||
		resp.StatusCode == http.StatusConflict ||
		resp.StatusCode == http.StatusTooManyRequests ||
		resp.StatusCode >= http.StatusInternalServerError
}

// idempotentPOST matches the paths of the POSTs that leave the same state
// when sent twice:
//   - token counting, which computes without storing anything
//     (https://platform.claude.com/docs/en/api/messages/count_tokens);
//   - archiving, pausing or unpausing an object, which sets its state;
//   - the update of an existing object whose update carries no
//     precondition: the body sets fields to the values it holds.
//
// The agent and memory updates are left out: they carry a version or
// content_sha256 precondition, so replaying one that landed answers 409,
// which reads as an out-of-band change. Creates, uploads, messages, other
// actions (a deployment run, a token rotation) and any path not named here
// keep the non-idempotent rule.
var idempotentPOST = regexp.MustCompile(`/v1/(` + strings.Join([]string{
	`messages/count_tokens`,
	`.+/(archive|pause|unpause)`,
	`(environments|vaults|memory_stores|deployments|user_profiles)/[^/]+`,
	`vaults/[^/]+/credentials/[^/]+`,
	`organizations/(service_accounts|federation_issuers|federation_rules)/[^/]+`,
}, "|") + `)$`)

// isIdempotent reports whether sending req twice leaves the same state as
// sending it once: the RFC 9110 section 9.2.2 methods, and the POSTs
// idempotentPOST matches.
func isIdempotent(req *http.Request) bool {
	switch strings.ToUpper(req.Method) {
	case http.MethodGet, http.MethodHead, http.MethodPut,
		http.MethodDelete, http.MethodOptions, http.MethodTrace:
		return true
	case http.MethodPost:
		return idempotentPOST.MatchString(req.URL.Path)
	default:
		return false
	}
}

// delay returns how long to wait before the next attempt. A retry-after
// header wins, bounded only by MaxRetryAfter: the server knows when the rate
// limit clears, and a shorter wait only earns another 429. Without it the
// delay doubles with each attempt up to MaxDelay, minus up to 25% of jitter.
func (p Policy) delay(resp *http.Response, attempt int) time.Duration {
	if d, ok := retryAfter(resp); ok {
		return min(max(0, d), MaxRetryAfter)
	}

	base := p.BaseDelay
	if base <= 0 {
		base = DefaultBaseDelay
	}
	maxDelay := p.MaxDelay
	if maxDelay <= 0 {
		maxDelay = DefaultMaxDelay
	}

	d := time.Duration(float64(base) * math.Pow(2, float64(attempt)))
	if d > maxDelay || d <= 0 {
		d = maxDelay
	}
	if quarter := int64(d / 4); quarter > 0 {
		d -= time.Duration(rand.Int63n(quarter)) // #nosec G404 -- jitter, not security
	}
	return d
}

// retryAfter reads the retry-after headers Anthropic returns on 429, in order
// of preference: retry-after-ms in milliseconds, then retry-after as a number
// of seconds or an HTTP-date. The result is not capped; see MaxRetryAfter.
func retryAfter(resp *http.Response) (time.Duration, bool) {
	if resp == nil {
		return 0, false
	}

	if v := resp.Header.Get("retry-after-ms"); v != "" {
		if ms, err := strconv.ParseFloat(v, 64); err == nil {
			return time.Duration(ms * float64(time.Millisecond)), true
		}
	}

	v := resp.Header.Get("retry-after")
	if v == "" {
		return 0, false
	}
	if seconds, err := strconv.ParseFloat(v, 64); err == nil {
		return time.Duration(seconds * float64(time.Second)), true
	}
	if t, err := http.ParseTime(v); err == nil {
		return time.Until(t), true
	}
	return 0, false
}
