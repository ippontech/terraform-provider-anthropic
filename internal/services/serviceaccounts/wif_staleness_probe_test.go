package serviceaccounts_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"

	"github.com/ippontech/terraform-provider-anthropic/internal/wifprobetest"
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
// `ANTHROPIC_WIF_STALENESS_PROBE=1` (wifprobetest.OptInEnvVar) opts in explicitly so a plain `make testacc`
// never runs it: every trial performs a real write and up to 5s of polling.
//
//	TF_ACC=1 ANTHROPIC_AUTH_TOKEN=... ANTHROPIC_WIF_STALENESS_PROBE=1 \
//	  go test -run TestAccWIFStalenessProbe -v ./internal/services/...
//
// Convergence is judged the way the production wait judges it: the read's
// updated_at is not older than the updated_at the write returned (non-strict,
// an equal timestamp counts), plus the mutated field carries the new value.
func TestAccWIFStalenessProbe(t *testing.T) {
	wifprobetest.PreCheck(t)

	ctx := context.Background()
	client := wifprobetest.NewClient()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	account, err := client.Beta.Organization.ServiceAccounts.New(ctx, anthropic.BetaOrganizationServiceAccountNewParams{
		Name: fmt.Sprintf("tf-probe-svc-%s", suffix),
	})
	if err != nil {
		wifprobetest.Fatal(t, "create service account", err)
	}
	t.Cleanup(func() {
		if _, err := client.Beta.Organization.ServiceAccounts.Archive(ctx, account.ID, anthropic.BetaOrganizationServiceAccountArchiveParams{}); err != nil {
			t.Logf("cleanup: archive service account %s: %s", account.ID, err)
		}
	})

	var results []wifprobetest.Result
	for trial := 1; trial <= wifprobetest.Trials; trial++ {
		res := wifprobetest.Result{Endpoint: "service_account GET after POST update", Trial: trial}
		want := fmt.Sprintf("probe trial %d", trial)
		writtenAt := wifprobetest.Write(t, &res, func() (time.Time, error) {
			updated, err := client.Beta.Organization.ServiceAccounts.Update(ctx, account.ID, anthropic.BetaOrganizationServiceAccountUpdateParams{
				Description: param.NewOpt(want),
			})
			if err != nil {
				return time.Time{}, err
			}
			return updated.UpdatedAt, nil
		})
		wifprobetest.Read(t, &res, writtenAt, func() (time.Time, bool, error) {
			got, err := client.Beta.Organization.ServiceAccounts.Get(ctx, account.ID, anthropic.BetaOrganizationServiceAccountGetParams{})
			if err != nil {
				return time.Time{}, false, err
			}
			return got.UpdatedAt, got.Description == want, nil
		})
		results = append(results, res)
	}

	wifprobetest.LogTable(t, results)
}
