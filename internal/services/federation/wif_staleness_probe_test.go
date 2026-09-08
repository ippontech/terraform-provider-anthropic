package federation_test

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

// TestAccWIFStalenessProbe measures read-after-write staleness on the WIF
// federation endpoints, the way the vaults API was probed on 2026-09-01 (see
// the "Read-after-write consistency" section of CLAUDE.md). It is an
// instrument, not a regression test: it never fails on a stale read, it only
// records when each endpoint converged, so the numbers can be copied into
// CLAUDE.md and the decision to add (or not add) a production wait rests on a
// measurement rather than on an analogy with vaults.
//
// It is doubly gated. acctest.PreCheckOAuth skips it without an org:admin
// bearer token (the federation endpoints reject API keys), and
// ANTHROPIC_WIF_STALENESS_PROBE=1 opts in explicitly so a plain `make testacc`
// never runs it: every trial performs a real write and up to 5s of polling.
//
//	TF_ACC=1 ANTHROPIC_AUTH_TOKEN=... ANTHROPIC_WIF_STALENESS_PROBE=1 \
//	  go test -run TestAccWIFStalenessProbe -v ./internal/services/...
//
// Each trial writes, then reads every wifProbeInterval until the read reflects
// the write or wifProbeTimeout elapses. Convergence is judged the way a
// production wait would judge it: the read's updated_at is not older than the
// updated_at the write itself returned (non-strict, an equal timestamp
// counts), plus the mutated field carries the new value.
//
// Everything created here is archived at the end, rule first: archiving an
// issuer or a service account still referenced by a live rule is a 400.
func TestAccWIFStalenessProbe(t *testing.T) {
	acctest.PreCheckOAuth(t)
	if os.Getenv("ANTHROPIC_WIF_STALENESS_PROBE") != "1" {
		t.Skip("set ANTHROPIC_WIF_STALENESS_PROBE=1 to run the read-after-write staleness probe")
	}

	ctx := context.Background()
	client := newWIFProbeClient()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	issuer, err := client.Beta.Organization.Federation.Issuers.New(ctx, anthropic.BetaOrganizationFederationIssuerNewParams{
		IssuerURL: fmt.Sprintf("https://tf-probe-%s.example.com", suffix),
		Name:      fmt.Sprintf("tf-probe-issuer-%s", suffix),
		JWKS: anthropic.BetaOrganizationFederationIssuerNewParamsJWKSUnion{
			OfInline: &anthropic.BetaJWKSInlineParam{Keys: []map[string]any{testFixtureRSAJWK}},
		},
	})
	if err != nil {
		wifProbeFatal(t, "create issuer", err)
	}
	t.Cleanup(func() {
		if _, err := client.Beta.Organization.Federation.Issuers.Archive(ctx, issuer.ID, anthropic.BetaOrganizationFederationIssuerArchiveParams{}); err != nil {
			t.Logf("cleanup: archive issuer %s: %s", issuer.ID, err)
		}
	})

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

	// POST /v1/organizations/federation_issuers/{id} then GET. Run before any
	// rule references the issuer, and once more afterwards (see below), so a
	// 404 from the write can be told apart from an "in use" refusal.
	probeIssuerUpdate := func(label string, trial int) {
		res := wifProbeResult{endpoint: label, trial: trial}
		want := fmt.Sprintf("tf-probe-issuer-%s-t%d", suffix, trial)
		writtenAt := probeWrite(t, &res, func() (time.Time, error) {
			updated, err := client.Beta.Organization.Federation.Issuers.Update(ctx, issuer.ID, anthropic.BetaOrganizationFederationIssuerUpdateParams{
				Name: param.NewOpt(want),
			})
			if err != nil {
				return time.Time{}, err
			}
			return updated.UpdatedAt, nil
		})
		probeRead(t, &res, writtenAt, func() (time.Time, bool, error) {
			got, err := client.Beta.Organization.Federation.Issuers.Get(ctx, issuer.ID, anthropic.BetaOrganizationFederationIssuerGetParams{})
			if err != nil {
				return time.Time{}, false, err
			}
			return got.UpdatedAt, got.Name == want, nil
		})
		results = append(results, res)
	}
	for trial := 1; trial <= wifProbeTrials; trial++ {
		probeIssuerUpdate("federation_issuer GET after POST update (no rule yet)", trial)
	}

	// The rule is bound to some other workspace at creation time so the
	// rule-workspace Add below enables a genuinely different one
	// (acctest.TerraformTestsWorkspaceID) instead of duplicating the
	// create-time binding.
	otherWorkspaceID := findOtherWorkspaceID(t, client, acctest.TerraformTestsWorkspaceID)
	rule, err := client.Beta.Organization.Federation.Rules.New(ctx, anthropic.BetaOrganizationFederationRuleNewParams{
		Name:       fmt.Sprintf("tf-probe-rule-%s", suffix),
		IssuerID:   issuer.ID,
		OAuthScope: "workspace:developer",
		Match: anthropic.BetaFederationRuleMatchParam{
			SubjectPrefix: param.NewOpt(fmt.Sprintf("repo:my-org/tf-probe-%s:*", suffix)),
		},
		Target:      anthropic.BetaServiceAccountTargetParam{ServiceAccountID: account.ID},
		WorkspaceID: param.NewOpt(otherWorkspaceID),
	})
	if err != nil {
		wifProbeFatal(t, "create rule", err)
	}
	// Registered after the issuer and service account cleanups, so it runs
	// first (t.Cleanup is LIFO): the rule must be gone before its targets.
	t.Cleanup(func() {
		if _, err := client.Beta.Organization.Federation.Rules.Archive(ctx, rule.ID, anthropic.BetaOrganizationFederationRuleArchiveParams{}); err != nil {
			t.Logf("cleanup: archive rule %s: %s", rule.ID, err)
		}
	})

	probeIssuerUpdate("federation_issuer GET after POST update (rule live)", wifProbeTrials+1)

	// POST /v1/organizations/federation_rules/{id} then GET.
	for trial := 1; trial <= wifProbeTrials; trial++ {
		res := wifProbeResult{endpoint: "federation_rule GET after POST update", trial: trial}
		want := fmt.Sprintf("probe trial %d", trial)
		writtenAt := probeWrite(t, &res, func() (time.Time, error) {
			updated, err := client.Beta.Organization.Federation.Rules.Update(ctx, rule.ID, anthropic.BetaOrganizationFederationRuleUpdateParams{
				Description: param.NewOpt(want),
			})
			if err != nil {
				return time.Time{}, err
			}
			return updated.UpdatedAt, nil
		})
		probeRead(t, &res, writtenAt, func() (time.Time, bool, error) {
			got, err := client.Beta.Organization.Federation.Rules.Get(ctx, rule.ID, anthropic.BetaOrganizationFederationRuleGetParams{})
			if err != nil {
				return time.Time{}, false, err
			}
			return got.UpdatedAt, got.Description == want, nil
		})
		results = append(results, res)
	}

	// POST /v1/organizations/federation_rules/{id}/workspaces (Add) then the
	// find-in-list GET the resource's Read performs, and the symmetric
	// Remove-then-list its CheckDestroy performs. The list entry carries no
	// updated_at, so convergence here is purely presence/absence and the
	// writtenAt handed to probeRead is zero.
	for trial := 1; trial <= wifProbeTrials; trial++ {
		add := wifProbeResult{endpoint: "federation_rule_workspaces LIST after POST Add", trial: trial}
		probeWrite(t, &add, func() (time.Time, error) {
			_, err := client.Beta.Organization.Federation.Rules.Workspaces.Add(ctx, rule.ID, anthropic.BetaOrganizationFederationRuleWorkspaceAddParams{
				WorkspaceID: acctest.TerraformTestsWorkspaceID,
			})
			return time.Time{}, err
		})
		probeRead(t, &add, time.Time{}, func() (time.Time, bool, error) {
			found, err := ruleWorkspaceListed(ctx, client, rule.ID, acctest.TerraformTestsWorkspaceID)
			return time.Time{}, found, err
		})
		results = append(results, add)

		remove := wifProbeResult{endpoint: "federation_rule_workspaces LIST after DELETE Remove", trial: trial}
		probeWrite(t, &remove, func() (time.Time, error) {
			_, err := client.Beta.Organization.Federation.Rules.Workspaces.Remove(ctx, acctest.TerraformTestsWorkspaceID, anthropic.BetaOrganizationFederationRuleWorkspaceRemoveParams{
				FederationRuleID: rule.ID,
			})
			return time.Time{}, err
		})
		probeRead(t, &remove, time.Time{}, func() (time.Time, bool, error) {
			found, err := ruleWorkspaceListed(ctx, client, rule.ID, acctest.TerraformTestsWorkspaceID)
			return time.Time{}, !found, err
		})
		results = append(results, remove)
	}

	logWIFProbeTable(t, results)
}

// ruleWorkspaceListed reports whether workspaceID appears in the rule's
// enabled-workspaces list, paging the way findFederationRuleWorkspace does.
func ruleWorkspaceListed(ctx context.Context, client *anthropic.Client, ruleID, workspaceID string) (bool, error) {
	pager := client.Beta.Organization.Federation.Rules.Workspaces.ListAutoPaging(ctx, ruleID, anthropic.BetaOrganizationFederationRuleWorkspaceListParams{})
	for pager.Next() {
		if pager.Current().WorkspaceID == workspaceID {
			return true, nil
		}
	}
	return false, pager.Err()
}

// --- probe harness -------------------------------------------------------
//
// Kept deliberately local to this file: it is an instrument for a one-off
// measurement, and the serviceaccounts package carries its own copy rather
// than growing a shared test-helper package for it.

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
