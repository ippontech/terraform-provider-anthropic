// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package retry

import (
	"io"
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
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
// Idempotent requests (GET, DELETE and the rest of the RFC 9110 set, plus
// the side-effect-free POST /v1/messages/count_tokens) keep the SDK's rule.
// Any other request, a POST that creates an object, uploads a file, sends a
// message or updates with a precondition, is replayed only on 429: the one
// answer that says the request was not processed. After a 5xx, a 409 or a
// dropped connection the write may have landed, and a replay would create a
// duplicate (an archive-only agent, a second file, a second billed message)
// or turn an applied precondition update into a misleading 409. The API
// documents no idempotency key that would make the replay safe.
//
// An x-should-retry header from the server settles the decision either way,
// as in the SDK. The delay honours retry-after-ms / retry-after, bounded by
// MaxRetryAfter, and otherwise backs off exponentially with jitter.
//
// Install it with SDKOptions, which turns the SDK's own retries off;
// otherwise both layers retry.
func Middleware(p Policy) option.Middleware {
	return func(req *http.Request, next option.MiddlewareNext) (*http.Response, error) {
		idempotent := isIdempotent(req)
		for attempt := 0; ; attempt++ {
			r := req
			if attempt > 0 {
				r = req.Clone(req.Context())
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
			}

			resp, err := next(r)
			if attempt >= p.MaxRetries || !replayable(req) || !shouldRetry(idempotent, resp) {
				return resp, err
			}
			if resp != nil && resp.Body != nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				_ = resp.Body.Close()
			}

			select {
			case <-req.Context().Done():
				return nil, req.Context().Err()
			case <-time.After(p.delay(resp, attempt)):
			}
		}
	}
}

// replayable reports whether the request body can be sent again. The SDK
// buffers JSON and multipart bodies alike and sets GetBody.
func replayable(req *http.Request) bool {
	return req.Body == nil || req.Body == http.NoBody || req.GetBody != nil
}

// shouldRetry decides whether a failed attempt is worth repeating; resp is
// nil after a connection error.
func shouldRetry(idempotent bool, resp *http.Response) bool {
	if resp != nil {
		switch resp.Header.Get("x-should-retry") {
		case "true":
			return true
		case "false":
			return false
		}
	}

	if !idempotent {
		return resp != nil && resp.StatusCode == http.StatusTooManyRequests
	}

	if resp == nil {
		return true
	}
	return resp.StatusCode == http.StatusRequestTimeout ||
		resp.StatusCode == http.StatusConflict ||
		resp.StatusCode == http.StatusTooManyRequests ||
		resp.StatusCode >= http.StatusInternalServerError
}

// isIdempotent reports whether sending req twice leaves the same state as
// sending it once: the RFC 9110 section 9.2.2 methods, and token counting,
// a POST that computes without storing anything
// (https://platform.claude.com/docs/en/api/messages/count_tokens).
func isIdempotent(req *http.Request) bool {
	switch strings.ToUpper(req.Method) {
	case http.MethodGet, http.MethodHead, http.MethodPut,
		http.MethodDelete, http.MethodOptions, http.MethodTrace:
		return true
	case http.MethodPost:
		return strings.HasSuffix(req.URL.Path, "/v1/messages/count_tokens")
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
