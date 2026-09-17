// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package wifprobetest holds the harness behind the TestAccWIFStalenessProbe
// tests in internal/services/federation and internal/services/serviceaccounts:
// they measure read-after-write staleness on the Workload Identity Federation
// endpoints (see the "Read-after-write consistency" section of CLAUDE.md). The
// harness is an instrument, not an assertion library: Read never fails a test
// on a stale read, it only records when the endpoint converged.
//
// Shared here, like internal/admintest, so a fix to the harness (the 401
// bail-out in Read, the polling bounds) lands once instead of drifting between
// per-package copies.
package wifprobetest

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

// Polling bounds shared by every probe: Trials writes per endpoint, each
// followed by a read every Interval until it converges or Timeout elapses.
const (
	Trials   = 3
	Timeout  = 5 * time.Second
	Interval = 50 * time.Millisecond
)

// OptInEnvVar is the environment variable that opts a run into the probe. It
// gates on top of acctest.PreCheckOAuth so a plain `make testacc` never runs
// it: every trial performs a real write and up to Timeout of polling.
const OptInEnvVar = "ANTHROPIC_WIF_STALENESS_PROBE"

// PreCheck skips the test unless an org:admin bearer token is configured
// (acctest.PreCheckOAuth) and OptInEnvVar is set to 1.
func PreCheck(t *testing.T) {
	t.Helper()
	acctest.PreCheckOAuth(t)
	if os.Getenv(OptInEnvVar) != "1" {
		t.Skipf("set %s=1 to run the read-after-write staleness probe", OptInEnvVar)
	}
}

// Result is one trial's outcome.
type Result struct {
	Endpoint string
	Trial    int
	// WriteNotFound counts the 404s the write itself answered before it was
	// accepted: an object that exists for one endpoint but "does not exist"
	// yet for another is read-after-write staleness on the write path.
	WriteNotFound int
	// WriteSettledAfter is the elapsed time until the write was accepted.
	WriteSettledAfter time.Duration
	// FirstReadLatency is how long the first read after the write took; with
	// StaleReads == 0 it is pure request latency, not a staleness window.
	FirstReadLatency time.Duration
	// StaleReads counts the reads that did not yet reflect the write.
	StaleReads int
	// ConvergedAfter is the elapsed time since the write returned at the
	// first read that reflected it; zero with Converged=false on timeout.
	ConvergedAfter time.Duration
	Converged      bool
}

// Write performs the write, retrying every Interval while it answers 404 (up
// to Timeout), and returns the updated_at it reported. Any other error, or a
// 404 that never clears, aborts the probe: the trial cannot be measured
// without a landed write.
func Write(t *testing.T, res *Result, write func() (time.Time, error)) time.Time {
	t.Helper()
	start := time.Now()
	deadline := start.Add(Timeout)
	for {
		writtenAt, err := write()
		if err == nil {
			res.WriteSettledAfter = time.Since(start)
			return writtenAt
		}
		var apierr *anthropic.Error
		if !errors.As(err, &apierr) || apierr.StatusCode != 404 || time.Now().After(deadline) {
			Fatal(t, fmt.Sprintf("%s trial %d write (after %d 404s)", res.Endpoint, res.Trial, res.WriteNotFound), err)
		}
		res.WriteNotFound++
		time.Sleep(Interval)
	}
}

// Read polls read until it reflects the write, judged by a non-strict
// updated_at comparison against writtenAt (skipped when writtenAt is zero, for
// list endpoints that carry no timestamp) and by the caller's own field
// check. A 401 aborts the whole test: the org:admin token is short-lived and
// retrying a dead credential would only produce a table of timeouts.
func Read(t *testing.T, res *Result, writtenAt time.Time, read func() (updatedAt time.Time, matches bool, err error)) {
	t.Helper()
	start := time.Now()
	deadline := start.Add(Timeout)

	for {
		updatedAt, matches, err := read()
		if res.FirstReadLatency == 0 {
			res.FirstReadLatency = time.Since(start)
		}
		switch {
		case err != nil:
			var apierr *anthropic.Error
			if errors.As(err, &apierr) && apierr.StatusCode == 401 {
				Fatal(t, res.Endpoint+" read", err)
			}
			t.Logf("%s trial %d: read error (counted as stale): %s", res.Endpoint, res.Trial, err)
			res.StaleReads++
		case matches && (writtenAt.IsZero() || !updatedAt.Before(writtenAt)):
			res.Converged = true
			res.ConvergedAfter = time.Since(start)
			return
		default:
			res.StaleReads++
		}

		if time.Now().After(deadline) {
			return
		}
		time.Sleep(Interval)
	}
}

// LogTable prints every trial as one row of a fixed-width table via t.Log, so
// the numbers can be copied into CLAUDE.md as they are.
func LogTable(t *testing.T, results []Result) {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "\nWIF read-after-write staleness probe (%d trials, %s interval, %s ceiling)\n", Trials, Interval, Timeout)
	fmt.Fprintf(&b, "%-56s %5s %10s %13s %10s %11s %15s\n", "endpoint", "trial", "write 404s", "write settled", "1st read", "stale reads", "converged after")
	for _, r := range results {
		conv := "TIMEOUT"
		if r.Converged {
			conv = r.ConvergedAfter.Round(time.Millisecond).String()
		}
		fmt.Fprintf(&b, "%-56s %5d %10d %13s %10s %11d %15s\n", r.Endpoint, r.Trial, r.WriteNotFound,
			r.WriteSettledAfter.Round(time.Millisecond), r.FirstReadLatency.Round(time.Millisecond), r.StaleReads, conv)
	}
	t.Log(b.String())
}

// NewClient builds an org:admin bearer client from ANTHROPIC_AUTH_TOKEN alone.
// WithoutEnvironmentDefaults keeps the SDK from also picking up
// ANTHROPIC_API_KEY or an ambient `ant` profile, either of which would make the
// endpoints answer 401 for reasons unrelated to the measurement.
func NewClient() *anthropic.Client {
	c := anthropic.NewClient(option.WithAuthToken(os.Getenv("ANTHROPIC_AUTH_TOKEN")), option.WithoutEnvironmentDefaults())
	return &c
}

// Fatal stops the probe with the failing step named. The error text is the
// SDK's, which never echoes the bearer token.
func Fatal(t *testing.T, step string, err error) {
	t.Helper()
	t.Fatalf("WIF staleness probe: %s failed: %s", step, err)
}
