// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package retry

import (
	"context"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const skillsRequestInterval = time.Minute / 90

// NewHTTPClient returns the shared HTTP client used by the Anthropic SDK.
// Skills requests are serialized at 90 requests/minute (10% below Anthropic's
// current 100 requests/minute organization limit), and replayable 429s are
// retried until their request context is cancelled or reaches its deadline.
func NewHTTPClient() *http.Client {
	return newHTTPClient(http.DefaultTransport, skillsRequestInterval)
}

func newHTTPClient(base http.RoundTripper, interval time.Duration) *http.Client {
	if base == nil {
		base = http.DefaultTransport
	}
	return &http.Client{Transport: &rateLimitTransport{
		base:          base,
		skillsLimiter: newSerialLimiter(interval),
	}}
}

type serialLimiter struct {
	mu       sync.Mutex
	next     time.Time
	interval time.Duration
}

func newSerialLimiter(interval time.Duration) *serialLimiter {
	return &serialLimiter{interval: interval}
}

func (l *serialLimiter) wait(ctx context.Context) error {
	l.mu.Lock()
	now := time.Now()
	start := now
	if l.next.After(start) {
		start = l.next
	}
	l.next = start.Add(l.interval)
	l.mu.Unlock()

	timer := time.NewTimer(time.Until(start))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

type rateLimitTransport struct {
	base          http.RoundTripper
	skillsLimiter *serialLimiter
}

func (t *rateLimitTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	for attempt := 0; ; attempt++ {
		if isSkillsRequest(req) {
			if err := t.skillsLimiter.wait(req.Context()); err != nil {
				return nil, err
			}
		}

		res, err := t.base.RoundTrip(req)
		if err != nil || res.StatusCode != http.StatusTooManyRequests || !replayable(req) {
			return res, err
		}

		delay := retryDelayFromHeaders(res.Header, time.Now())
		if delay <= 0 {
			delay = exponentialBackoff(attempt)
		} else {
			// Never retry before the server-requested time; positive jitter keeps
			// concurrent Terraform operations from waking in lockstep.
			delay += time.Duration(rand.Float64() * float64(delay) / 4)
		}
		if deadline, ok := req.Context().Deadline(); ok && time.Now().Add(delay).After(deadline) {
			_, _ = io.Copy(io.Discard, res.Body)
			_ = res.Body.Close()
			if err := waitForRetry(req.Context(), time.Until(deadline)); err != nil {
				return nil, err
			}
			return nil, context.DeadlineExceeded
		}

		_, _ = io.Copy(io.Discard, res.Body)
		_ = res.Body.Close()
		if err := waitForRetry(req.Context(), delay); err != nil {
			return nil, err
		}
		if req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			req.Body = body
		}
	}
}

func isSkillsRequest(req *http.Request) bool {
	return req.URL != nil && (req.URL.Path == "/v1/skills" || strings.HasPrefix(req.URL.Path, "/v1/skills/"))
}

func replayable(req *http.Request) bool {
	return req.Body == nil || req.GetBody != nil
}

func waitForRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func exponentialBackoff(attempt int) time.Duration {
	delay := time.Second
	for range min(attempt, 5) {
		delay *= 2
	}
	// 75%-100% jitter prevents synchronization while retaining exponential growth.
	return time.Duration(float64(delay) * (0.75 + rand.Float64()*0.25))
}

func retryDelayFromHeaders(headers http.Header, now time.Time) time.Duration {
	if value := headers.Get("Retry-After-Ms"); value != "" {
		if milliseconds, err := strconv.ParseFloat(value, 64); err == nil {
			return max(0, time.Duration(milliseconds*float64(time.Millisecond)))
		}
	}
	if value := headers.Get("Retry-After"); value != "" {
		if seconds, err := strconv.ParseFloat(value, 64); err == nil {
			return max(0, time.Duration(seconds*float64(time.Second)))
		}
		if reset, err := http.ParseTime(value); err == nil {
			return max(0, reset.Sub(now))
		}
	}

	// Anthropic exposes quota-family headers such as
	// anthropic-ratelimit-requests-remaining/reset. The Skills API may use a
	// distinct family, so match any exhausted "*-remaining" header with its
	// sibling "*-reset" header instead of hard-coding one quota name.
	for name, values := range headers {
		if !strings.HasSuffix(strings.ToLower(name), "-remaining") || len(values) == 0 {
			continue
		}
		remaining, err := strconv.ParseFloat(values[0], 64)
		if err != nil || remaining > 0 {
			continue
		}
		resetName := name[:len(name)-len("remaining")] + "reset"
		if delay, ok := parseReset(headers.Get(resetName), now); ok {
			return delay
		}
	}
	return 0
}

func parseReset(value string, now time.Time) (time.Duration, bool) {
	if reset, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return max(0, reset.Sub(now)), true
	}
	if unix, err := strconv.ParseFloat(value, 64); err == nil {
		return max(0, time.Unix(0, int64(unix*float64(time.Second))).Sub(now)), true
	}
	return 0, false
}
