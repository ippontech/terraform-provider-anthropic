package serviceaccounts_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"

	"github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

// TestAccWIFStalenessProbe measures read-after-write staleness on
// GET /v1/organizations/service_accounts/{id} after POST …/{id}, the way the
// vaults API was probed on 2026-09-01 (see the "Read-after-write consistency"
// section of CLAUDE.md). anthropic_service_account's Update already applies
// the vaults-style wait defensively; this probe is what turns that analogy
// into a measurement. It is an instrument, not a regression test: it never
// fails on a stale read, it only records when the endpoint converged. Its
// sibling in internal/services/federation probes the issuer, rule and
// rule-workspace endpoints.
//
// It is doubly gated. acctest.PreCheckOAuth skips it without an org:admin
// bearer token (the endpoint rejects API keys), and
// ANTHROPIC_WIF_STALENESS_PROBE=1 opts in explicitly so a plain `make testacc`
// never runs it: every trial performs a real write and up to 5s of polling.
//
//	TF_ACC=1 ANTHROPIC_AUTH_TOKEN=... ANTHROPIC_WIF_STALENESS_PROBE=1 \
//	  go test -run TestAccWIFStalenessProbe -v ./internal/services/...
//
// Convergence is judged the way the production wait judges it: the read's
// updated_at is not older than the updated_at the write returned (non-strict,
// an equal timestamp counts), plus the mutated field carries the new value.
func TestAccWIFStalenessProbe(t *testing.T) {
	acctest.PreCheckOAuth(t)
	if os.Getenv("ANTHROPIC_WIF_STALENESS_PROBE") != "1" {
		t.Skip("set ANTHROPIC_WIF_STALENESS_PROBE=1 to run the read-after-write staleness probe")
	}

	ctx := context.Background()
	client := newWIFProbeClient()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	account, err := client.Beta.Organization.ServiceAccounts.New(ctx, anthropic.BetaOrganizationServiceAccountNewParams{
		Name: fmt.Sprintf("tf-probe-svc-%s", suffix),
	})
	if err != nil {
		wifProbeFatal(t, "create service account", err)
	}
	t.Cleanup(func() {
		if _, err := client.Beta.Organization.ServiceAccounts.Archive(ctx, account.ID, anthropic.BetaOrganizationServiceAccountArchiveParams{}); err != nil {
			t.Logf("cleanup: archive service account %s: %s", account.ID, err)
		}
	})

	var results []wifProbeResult
	for trial := 1; trial <= wifProbeTrials; trial++ {
		res := wifProbeResult{endpoint: "service_account GET after POST update", trial: trial}
		want := fmt.Sprintf("probe trial %d", trial)
		writtenAt := probeWrite(t, &res, func() (time.Time, error) {
			updated, err := client.Beta.Organization.ServiceAccounts.Update(ctx, account.ID, anthropic.BetaOrganizationServiceAccountUpdateParams{
				Description: param.NewOpt(want),
			})
			if err != nil {
				return time.Time{}, err
			}
			return updated.UpdatedAt, nil
		})
		probeRead(t, &res, writtenAt, func() (time.Time, bool, error) {
			got, err := client.Beta.Organization.ServiceAccounts.Get(ctx, account.ID, anthropic.BetaOrganizationServiceAccountGetParams{})
			if err != nil {
				return time.Time{}, false, err
			}
			return got.UpdatedAt, got.Description == want, nil
		})
		results = append(results, res)
	}

	logWIFProbeTable(t, results)
}

// --- probe harness -------------------------------------------------------
//
// A copy of the harness in internal/services/federation's probe file, kept
// local on purpose: it is an instrument for a one-off measurement, not worth a
// shared test-helper package.

const (
	wifProbeTrials   = 3
	wifProbeTimeout  = 5 * time.Second
	wifProbeInterval = 50 * time.Millisecond
)

// wifProbeResult is one trial's outcome.
type wifProbeResult struct {
	endpoint string
	trial    int
	// writeNotFound counts the 404s the write itself answered before it was
	// accepted: an object that exists for one endpoint but "does not exist"
	// yet for another is read-after-write staleness on the write path.
	writeNotFound int
	// writeSettledAfter is the elapsed time until the write was accepted.
	writeSettledAfter time.Duration
	// firstReadLatency is how long the first read after the write took; with
	// staleReads == 0 it is pure request latency, not a staleness window.
	firstReadLatency time.Duration
	// staleReads counts the reads that did not yet reflect the write.
	staleReads int
	// convergedAfter is the elapsed time since the write returned at the
	// first read that reflected it; zero with converged=false on timeout.
	convergedAfter time.Duration
	converged      bool
}

// probeWrite performs the write, retrying every wifProbeInterval while it
// answers 404 (up to wifProbeTimeout), and returns the updated_at it reported.
// Any other error, or a 404 that never clears, aborts the probe: the trial
// cannot be measured without a landed write.
func probeWrite(t *testing.T, res *wifProbeResult, write func() (time.Time, error)) time.Time {
	t.Helper()
	start := time.Now()
	deadline := start.Add(wifProbeTimeout)
	for {
		writtenAt, err := write()
		if err == nil {
			res.writeSettledAfter = time.Since(start)
			return writtenAt
		}
		var apierr *anthropic.Error
		if !errors.As(err, &apierr) || apierr.StatusCode != 404 || time.Now().After(deadline) {
			wifProbeFatal(t, fmt.Sprintf("%s trial %d write (after %d 404s)", res.endpoint, res.trial, res.writeNotFound), err)
		}
		res.writeNotFound++
		time.Sleep(wifProbeInterval)
	}
}

// probeRead polls read until it reflects the write, judged by a non-strict
// updated_at comparison against writtenAt (skipped when writtenAt is zero, for
// list endpoints that carry no timestamp) and by the caller's own field
// check. A 401 aborts the whole test: the org:admin token is short-lived and
// retrying a dead credential would only produce a table of timeouts.
func probeRead(t *testing.T, res *wifProbeResult, writtenAt time.Time, read func() (updatedAt time.Time, matches bool, err error)) {
	t.Helper()
	start := time.Now()
	deadline := start.Add(wifProbeTimeout)

	for {
		updatedAt, matches, err := read()
		if res.firstReadLatency == 0 {
			res.firstReadLatency = time.Since(start)
		}
		switch {
		case err != nil:
			var apierr *anthropic.Error
			if errors.As(err, &apierr) && apierr.StatusCode == 401 {
				wifProbeFatal(t, res.endpoint+" read", err)
			}
			t.Logf("%s trial %d: read error (counted as stale): %s", res.endpoint, res.trial, err)
			res.staleReads++
		case matches && (writtenAt.IsZero() || !updatedAt.Before(writtenAt)):
			res.converged = true
			res.convergedAfter = time.Since(start)
			return
		default:
			res.staleReads++
		}

		if time.Now().After(deadline) {
			return
		}
		time.Sleep(wifProbeInterval)
	}
}

func logWIFProbeTable(t *testing.T, results []wifProbeResult) {
	t.Helper()
	var b strings.Builder
	fmt.Fprintf(&b, "\nWIF read-after-write staleness probe (%d trials, %s interval, %s ceiling)\n", wifProbeTrials, wifProbeInterval, wifProbeTimeout)
	fmt.Fprintf(&b, "%-56s %5s %10s %13s %10s %11s %15s\n", "endpoint", "trial", "write 404s", "write settled", "1st read", "stale reads", "converged after")
	for _, r := range results {
		conv := "TIMEOUT"
		if r.converged {
			conv = r.convergedAfter.Round(time.Millisecond).String()
		}
		fmt.Fprintf(&b, "%-56s %5d %10d %13s %10s %11d %15s\n", r.endpoint, r.trial, r.writeNotFound,
			r.writeSettledAfter.Round(time.Millisecond), r.firstReadLatency.Round(time.Millisecond), r.staleReads, conv)
	}
	t.Log(b.String())
}

// newWIFProbeClient builds an org:admin bearer client from ANTHROPIC_AUTH_TOKEN
// alone. WithoutEnvironmentDefaults keeps the SDK from also picking up
// ANTHROPIC_API_KEY or an ambient `ant` profile, either of which would make the
// endpoints answer 401 for reasons unrelated to the measurement.
func newWIFProbeClient() *anthropic.Client {
	c := anthropic.NewClient(option.WithAuthToken(os.Getenv("ANTHROPIC_AUTH_TOKEN")), option.WithoutEnvironmentDefaults())
	return &c
}

// wifProbeFatal stops the probe with the failing step named. The error text is
// the SDK's, which never echoes the bearer token.
func wifProbeFatal(t *testing.T, step string, err error) {
	t.Helper()
	t.Fatalf("WIF staleness probe: %s failed: %s", step, err)
}
