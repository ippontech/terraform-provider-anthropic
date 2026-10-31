// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package admin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

const (
	adminAPIBaseURL  = "https://api.anthropic.com"
	AnthropicVersion = "2023-06-01"

	// DefaultMaxRetries mirrors the Anthropic Go SDK, which retries twice after
	// the initial attempt.
	DefaultMaxRetries = 2
	// DefaultBaseRetryDelay is the first backoff step; each subsequent retry
	// doubles it, up to DefaultMaxRetryDelay.
	DefaultBaseRetryDelay = 500 * time.Millisecond
	// DefaultMaxRetryDelay caps the exponential backoff.
	DefaultMaxRetryDelay = 8 * time.Second

	// maxRetryAfter caps how long a server-supplied retry-after header can hold
	// up an apply. The SDK honours the header verbatim; a Terraform provider
	// that blocked for an hour on a stray value would be worse than giving up.
	maxRetryAfter = 60 * time.Second

	// attemptTimeout bounds one attempt, response body included, so that a
	// server that accepts the connection and never answers fails the attempt
	// instead of hanging a plan or an apply.
	attemptTimeout = 60 * time.Second
)

// Client makes authenticated requests to the Anthropic Admin API (/v1/organizations/*).
// The provider's Admin API resources send raw JSON through this client rather
// than through the SDK's organization services.
//
// Failed requests are retried with exponential backoff that honours the
// retry-after headers Anthropic returns on 429. How much is retried depends on
// the method: idempotent requests replay on connection errors and on the
// transient statuses the SDK retries (408, 409, 429, and 5xx), while a POST
// replays only on 429 — see shouldRetry. Retries are opt-in: the zero value
// performs a single attempt, and NewClient enables DefaultMaxRetries.
//
// A client built by NewSDKClient sends its requests through an SDK client
// instead of HTTPClient, with the same retry policy; see NewSDKClient.
type Client struct {
	ApiKey     string
	BaseURL    string
	HTTPClient *http.Client

	// MaxRetries is the number of retries attempted after the initial request.
	// Zero (the zero value) disables retrying entirely.
	MaxRetries int
	// BaseRetryDelay is the first backoff step. Zero means DefaultBaseRetryDelay.
	BaseRetryDelay time.Duration
	// MaxRetryDelay caps the exponential backoff. Zero means DefaultMaxRetryDelay.
	// It does not bound a server-supplied retry-after delay, which is honoured as
	// sent and bounded only by maxRetryAfter.
	MaxRetryDelay time.Duration

	// sdk, when set, carries every request instead of HTTPClient, with its own
	// base URL and authentication; ApiKey and BaseURL are then unused.
	sdk *anthropic.Client
	// sdkAttemptTimeout bounds each attempt through sdk, as HTTPClient's
	// Timeout does for the direct client. Zero means no bound.
	sdkAttemptTimeout time.Duration
}

// APIError is returned when the API responds with a non-2xx status code.
type APIError struct {
	StatusCode int
	ErrType    string
	Message    string
}

func (e *APIError) Error() string {
	if e.ErrType != "" {
		return fmt.Sprintf("API error (%d %s): %s", e.StatusCode, e.ErrType, e.Message)
	}
	return fmt.Sprintf("API error (%d): %s", e.StatusCode, e.Message)
}

// IsNotFound returns true when err is an APIError with status 404.
func IsNotFound(err error) bool {
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.StatusCode == 404
}

func NewClient(apiKey string) *Client {
	return &Client{
		ApiKey:     apiKey,
		BaseURL:    adminAPIBaseURL,
		HTTPClient: &http.Client{Timeout: attemptTimeout},
		MaxRetries: DefaultMaxRetries,
	}
}

// NewSDKClient returns an Admin API client that sends every request through
// sdk, the provider's single client for a cloud gateway platform (Claude
// Platform on AWS, Claude Models on Google Cloud). The direct client cannot
// serve them: it sends x-api-key to api.anthropic.com, while a gateway has its
// own base URL (per region, or per project and route) and signs each request
// (AWS SigV4) or carries a Google access token. Going through sdk, the request
// gets all of that, and the middlewares the provider installed on sdk, such as
// platform.NotFoundGuard.
//
// The retry policy stays this package's: the SDK's own retries are turned off
// for each request, so a POST is still replayed only on 429. The SDK alone
// would replay a POST on a 5xx or a dropped connection, and a second
// POST /v1/organizations/workspaces creates a second workspace.
func NewSDKClient(sdk *anthropic.Client) *Client {
	return &Client{sdk: sdk, MaxRetries: DefaultMaxRetries, sdkAttemptTimeout: attemptTimeout}
}

// DoRequest executes an HTTP request against the Admin API and returns the raw response body.
func (c *Client) DoRequest(ctx context.Context, method, path string, body any) ([]byte, error) {
	var reqBody []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		reqBody = b
	}

	// Built once: a malformed method or URL is a programmer error, not
	// something a retry can fix.
	req, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("create HTTP request: %w", err)
	}
	if c.sdk == nil {
		req.Header.Set("x-api-key", c.ApiKey)
	}
	req.Header.Set("anthropic-version", AnthropicVersion)
	if reqBody != nil {
		req.Header.Set("content-type", "application/json")
	}

	for attempt := 0; ; attempt++ {
		respBody, resp, err := c.attempt(req, reqBody)
		if err == nil {
			return respBody, nil
		}

		if attempt >= c.MaxRetries || !shouldRetry(req.Method, resp) {
			return nil, err
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(c.retryDelay(resp, attempt)):
		}
	}
}

// attempt performs a single HTTP round trip, giving the request a fresh reader
// over reqBody so it can be replayed. It returns the response body on success,
// and on failure the error alongside the response that produced it — nil
// whenever nothing usable came back, which covers both a connection error and a
// body that stopped arriving mid-read — so the caller can decide whether to
// retry.
func (c *Client) attempt(template *http.Request, reqBody []byte) ([]byte, *http.Response, error) {
	if c.sdk != nil {
		return c.sdkAttempt(template, reqBody)
	}

	req := template.Clone(template.Context())
	if reqBody != nil {
		req.Body = io.NopCloser(bytes.NewReader(reqBody))
		req.ContentLength = int64(len(reqBody))
		// net/http replays the body itself for an HTTP/2 GOAWAY or a 307/308
		// redirect, but only when it can rewind it. A bare NopCloser gives it
		// nothing to introspect, so hand it the rewind explicitly.
		req.GetBody = func() (io.ReadCloser, error) {
			return io.NopCloser(bytes.NewReader(reqBody)), nil
		}
	}

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, nil, fmt.Errorf("execute HTTP request: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		// The status line arrived but the payload did not: a truncated body is a
		// transport failure, not a verdict from the API. Reporting it with a nil
		// response keeps the retry decision off a status code whose body never
		// landed — and matches how a connection error is reported.
		return nil, nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp, newAPIError(resp.StatusCode, respBody)
	}

	return respBody, resp, nil
}

// sdkAttempt is attempt through c.sdk. The template's method, path, query and
// headers go out as they are, except anthropic-version, which the SDK sets to
// the same value, and the body is sent verbatim. It returns what attempt does:
// an *APIError built from the same body on an error status, and the response
// behind any error so that shouldRetry sees its status and x-should-retry
// header. That covers platform.NotFoundGuard's error, whose response says not
// to retry; a transport error has no response, as with HTTPClient.
func (c *Client) sdkAttempt(template *http.Request, reqBody []byte) ([]byte, *http.Response, error) {
	var res *http.Response
	opts := []option.RequestOption{
		option.WithMaxRetries(0),
		// The SDK sets no per-request deadline by default. With this one, a
		// stalled attempt fails like one of the direct client and goes back
		// to DoRequest's retry loop (GET and DELETE retry it, POST does not).
		option.WithRequestTimeout(c.sdkAttemptTimeout),
		option.WithResponseInto(&res),
		// The Admin API is organization-wide. Claude Platform on AWS sets a
		// workspace header on every request of its SDK client, which its
		// Admin API routes do not require; like the direct client, this one
		// sends none. A template header, set by the caller, still goes out.
		option.WithHeaderDel("anthropic-workspace-id"),
	}
	for name, values := range template.Header {
		if http.CanonicalHeaderKey(name) == "Anthropic-Version" {
			continue
		}
		for i, v := range values {
			if i == 0 {
				opts = append(opts, option.WithHeader(name, v))
			} else {
				opts = append(opts, option.WithHeaderAdd(name, v))
			}
		}
	}

	// A nil []byte would still be sent as an empty JSON body.
	var params any
	if reqBody != nil {
		params = reqBody
	}

	// Relative to the SDK's base URL, which on a gateway carries a path of its
	// own (/v1/projects/.../invoke on Google Cloud): a leading slash would
	// resolve against the host instead.
	path := strings.TrimPrefix(template.URL.RequestURI(), "/")

	var body []byte
	err := c.sdk.Execute(template.Context(), template.Method, path, params, &body, opts...)
	if err != nil {
		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) {
			return nil, apiErr.Response, newAPIError(apiErr.StatusCode, []byte(apiErr.RawJSON()))
		}
		return nil, res, err
	}
	return body, res, nil
}

func newAPIError(statusCode int, respBody []byte) *APIError {
	var envelope struct {
		Error struct {
			Type    string `json:"type"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(respBody, &envelope) == nil && envelope.Error.Message != "" {
		return &APIError{
			StatusCode: statusCode,
			ErrType:    envelope.Error.Type,
			Message:    envelope.Error.Message,
		}
	}
	return &APIError{
		StatusCode: statusCode,
		Message:    string(respBody),
	}
}

// shouldRetry reports whether a failed attempt is worth repeating. As in the
// Anthropic Go SDK, an x-should-retry header from the server settles it either
// way. Beyond that the answer depends on whether replaying the request is safe.
//
// Idempotent requests replay freely: a nil response (a connection error, or a
// body that never finished arriving) and the transient statuses the SDK retries
// — 408, 409, 429 and 5xx — are all worth another attempt.
//
// A non-idempotent request replays only on 429. POST is how the Admin API
// creates a workspace and adds a member, and 429 is the one answer that states
// the call was not processed. On a 5xx, a 409 or a dropped connection the write
// may well have landed: replaying it would create a second workspace (names are
// not unique) or burn the budget on a membership that already exists, leaving
// the resource untracked either way. The SDK retries those because its POSTs are
// inference calls, where a duplicate costs tokens rather than resources.
func shouldRetry(method string, resp *http.Response) bool {
	if resp != nil {
		switch resp.Header.Get("x-should-retry") {
		case "true":
			return true
		case "false":
			return false
		}
	}

	if !isIdempotent(method) {
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

// isIdempotent reports whether repeating a request with this method leaves the
// same state as sending it once, per RFC 9110 section 9.2.2. The Admin API only
// ever sees GET, POST and DELETE, but the whole set is listed so the client
// stays correct if that changes.
func isIdempotent(method string) bool {
	switch strings.ToUpper(method) {
	case http.MethodGet, http.MethodHead, http.MethodPut,
		http.MethodDelete, http.MethodOptions, http.MethodTrace:
		return true
	default:
		return false
	}
}

// retryDelay returns how long to wait before the next attempt. A retry-after
// header from the server wins, deliberately unbounded by MaxRetryDelay: the
// server knows when the rate limit clears, and shortening its answer only earns
// another 429. Only maxRetryAfter bounds it. Absent the header the delay doubles
// with each attempt, capped by MaxRetryDelay and then reduced by up to 25% of
// jitter to spread out concurrent retries.
func (c *Client) retryDelay(resp *http.Response, attempt int) time.Duration {
	if d, ok := parseRetryAfter(resp); ok {
		return min(max(0, d), maxRetryAfter)
	}

	base := c.BaseRetryDelay
	if base <= 0 {
		base = DefaultBaseRetryDelay
	}
	maxDelay := c.MaxRetryDelay
	if maxDelay <= 0 {
		maxDelay = DefaultMaxRetryDelay
	}

	delay := time.Duration(float64(base) * math.Pow(2, float64(attempt)))
	if delay > maxDelay || delay <= 0 {
		delay = maxDelay
	}

	if quarter := int64(delay / 4); quarter > 0 {
		delay -= time.Duration(rand.Int63n(quarter)) // #nosec G404 -- jitter, not security
	}
	return delay
}

// parseRetryAfter reads the retry-after headers Anthropic returns on 429, in
// order of preference: retry-after-ms in milliseconds, then retry-after as
// either a number of seconds or an HTTP-date.
func parseRetryAfter(resp *http.Response) (time.Duration, bool) {
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
