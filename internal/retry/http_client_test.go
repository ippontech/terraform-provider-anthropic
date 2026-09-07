// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package retry

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

func TestSkillsReadAndDeleteRetry429(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			var calls atomic.Int32
			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != method {
					t.Errorf("method = %s, want %s", r.Method, method)
				}
				if got, want := r.URL.Path, "/v1/skills/skill_123/versions/1"; got != want {
					t.Errorf("path = %s, want %s", got, want)
				}
				if calls.Add(1) == 1 {
					w.Header().Set("Retry-After-Ms", "1")
					http.Error(w, `{"type":"error","error":{"type":"rate_limit_error","message":"limited"}}`, http.StatusTooManyRequests)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, `{}`)
			})

			client := anthropic.NewClient(
				option.WithAPIKey("test"),
				option.WithBaseURL("https://anthropic.test"),
				option.WithHTTPClient(newHTTPClient(handlerTransport(handler), 0)),
				option.WithMaxRetries(0),
			)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if method == http.MethodGet {
				_, err := client.Beta.Skills.Versions.Get(ctx, "1", anthropic.BetaSkillVersionGetParams{SkillID: "skill_123"})
				if err != nil {
					t.Fatalf("Read after 429: %v", err)
				}
			} else {
				_, err := client.Beta.Skills.Versions.Delete(ctx, "1", anthropic.BetaSkillVersionDeleteParams{SkillID: "skill_123"})
				if err != nil {
					t.Fatalf("Delete after 429: %v", err)
				}
			}
			if got := calls.Load(); got != 2 {
				t.Fatalf("calls = %d, want 2", got)
			}
		})
	}
}

func TestSkillsRequestsAreLimitedAcrossConcurrentCalls(t *testing.T) {
	const (
		requests = 6
		interval = 15 * time.Millisecond
	)
	var (
		mu     sync.Mutex
		starts []time.Time
	)
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		starts = append(starts, time.Now())
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `{}`)
	})

	client := anthropic.NewClient(
		option.WithAPIKey("test"),
		option.WithBaseURL("https://anthropic.test"),
		option.WithHTTPClient(newHTTPClient(handlerTransport(handler), interval)),
		option.WithMaxRetries(0),
	)
	var wg sync.WaitGroup
	errs := make(chan error, requests)
	for i := range requests {
		wg.Add(1)
		go func(version int) {
			defer wg.Done()
			_, err := client.Beta.Skills.Versions.Get(context.Background(), fmt.Sprint(version), anthropic.BetaSkillVersionGetParams{SkillID: "skill_123"})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("concurrent Read: %v", err)
		}
	}

	mu.Lock()
	slices.SortFunc(starts, func(a, b time.Time) int { return a.Compare(b) })
	gotStarts := append([]time.Time(nil), starts...)
	mu.Unlock()
	if len(gotStarts) != requests {
		t.Fatalf("request starts = %d, want %d", len(gotStarts), requests)
	}
	for i := 1; i < len(gotStarts); i++ {
		if gap := gotStarts[i].Sub(gotStarts[i-1]); gap < interval-time.Millisecond {
			t.Errorf("requests %d and %d started %s apart, want at least %s", i-1, i, gap, interval-time.Millisecond)
		}
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return fn(req) }

func handlerTransport(handler http.Handler) http.RoundTripper {
	return roundTripFunc(func(req *http.Request) (*http.Response, error) {
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		return recorder.Result(), nil
	})
}

func TestRetryDelayFromExhaustedQuotaReset(t *testing.T) {
	now := time.Date(2026, 8, 27, 0, 0, 0, 0, time.UTC)
	headers := http.Header{
		"Anthropic-Ratelimit-Skills-Remaining": []string{"0"},
		"Anthropic-Ratelimit-Skills-Reset":     []string{now.Add(3 * time.Second).Format(time.RFC3339Nano)},
	}
	if got := retryDelayFromHeaders(headers, now); got != 3*time.Second {
		t.Fatalf("retry delay = %s, want 3s", got)
	}
}
