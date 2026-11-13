// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package retry

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
)

const (
	// createRetries is how many times a create answered 429 is sent again:
	// as many as the SDK's default retries.
	createRetries = 2
	// maxCreateRetryAfter bounds the wait a retry-after header can ask for.
	maxCreateRetryAfter = 60 * time.Second
	// createRetryDelay is the wait before a retry when the 429 carries no
	// retry-after header; it doubles with each retry.
	createRetryDelay = 500 * time.Millisecond
)

// CreateRetrying429 runs create, a POST that is not idempotent and so is sent
// with option.WithMaxRetries(0), and sends it again only when the API answers
// 429. A 429 states that the request was not processed, whereas a 408, a 409,
// a 5xx or a dropped connection may follow a create that landed, and
// replaying it would leave a duplicate Terraform does not track. Without the
// retry, an object created while Terraform runs other creates in parallel
// would fail on the first 429 (for a tunnel, after a replacement had already
// archived the old object).
func CreateRetrying429[T any](ctx context.Context, create func() (T, error)) (T, error) {
	for retry := 0; ; retry++ {
		v, err := create()
		var apierr *anthropic.Error
		if err == nil || retry == createRetries || !errors.As(err, &apierr) || !retryableCreate(apierr) {
			return v, err
		}
		timer := time.NewTimer(createRetryWait(apierr.Response, retry))
		select {
		case <-ctx.Done():
			timer.Stop()
			return v, err
		case <-timer.C:
		}
	}
}

// retryableCreate reports whether a failed create can be sent again: only on
// a 429, and not when the server adds x-should-retry: false. Unlike the SDK,
// which also retries when x-should-retry is true, the header never makes
// another status retryable: a 5xx or a 409 may follow a create that landed,
// whatever the server suggests.
func retryableCreate(apierr *anthropic.Error) bool {
	if apierr.StatusCode != http.StatusTooManyRequests {
		return false
	}
	return apierr.Response == nil || apierr.Response.Header.Get("x-should-retry") != "false"
}

// createRetryWait returns the wait the server asks for in retry-after-ms or
// retry-after (seconds or an HTTP date), bounded by maxCreateRetryAfter, or
// else createRetryDelay doubled for each earlier retry.
func createRetryWait(resp *http.Response, retry int) time.Duration {
	if resp != nil {
		if ms, err := strconv.ParseFloat(resp.Header.Get("retry-after-ms"), 64); err == nil {
			return min(max(0, time.Duration(ms*float64(time.Millisecond))), maxCreateRetryAfter)
		}
		v := resp.Header.Get("retry-after")
		if s, err := strconv.ParseFloat(v, 64); err == nil {
			return min(max(0, time.Duration(s*float64(time.Second))), maxCreateRetryAfter)
		}
		if t, err := http.ParseTime(v); err == nil {
			return min(max(0, time.Until(t)), maxCreateRetryAfter)
		}
	}
	return createRetryDelay << retry
}
