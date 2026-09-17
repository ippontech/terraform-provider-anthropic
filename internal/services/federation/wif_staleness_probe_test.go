package federation_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"

	"github.com/ippontech/terraform-provider-anthropic/internal/acctest"
	"github.com/ippontech/terraform-provider-anthropic/internal/wifprobetest"
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
// `ANTHROPIC_WIF_STALENESS_PROBE=1` (wifprobetest.OptInEnvVar) opts in explicitly so a plain `make testacc`
// never runs it: every trial performs a real write and up to 5s of polling.
//
//	TF_ACC=1 ANTHROPIC_AUTH_TOKEN=... ANTHROPIC_WIF_STALENESS_PROBE=1 \
//	  go test -run TestAccWIFStalenessProbe -v ./internal/services/...
//
// Each trial writes, then reads every wifprobetest.Interval until the read reflects
// the write or wifprobetest.Timeout elapses. Convergence is judged the way a
// production wait would judge it: the read's updated_at is not older than the
// updated_at the write itself returned (non-strict, an equal timestamp
// counts), plus the mutated field carries the new value.
//
// Everything created here is archived at the end, rule first: archiving an
// issuer or a service account still referenced by a live rule is a 400.
func TestAccWIFStalenessProbe(t *testing.T) {
	wifprobetest.PreCheck(t)

	ctx := context.Background()
	client := wifprobetest.NewClient()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	issuer, err := client.Beta.Organization.Federation.Issuers.New(ctx, anthropic.BetaOrganizationFederationIssuerNewParams{
		IssuerURL: fmt.Sprintf("https://tf-probe-%s.example.com", suffix),
		Name:      fmt.Sprintf("tf-probe-issuer-%s", suffix),
		JWKS: anthropic.BetaOrganizationFederationIssuerNewParamsJWKSUnion{
			OfInline: &anthropic.BetaJWKSInlineParam{Keys: []map[string]any{testFixtureRSAJWK}},
		},
	})
	if err != nil {
		wifprobetest.Fatal(t, "create issuer", err)
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
		wifprobetest.Fatal(t, "create service account", err)
	}
	t.Cleanup(func() {
		if _, err := client.Beta.Organization.ServiceAccounts.Archive(ctx, account.ID, anthropic.BetaOrganizationServiceAccountArchiveParams{}); err != nil {
			t.Logf("cleanup: archive service account %s: %s", account.ID, err)
		}
	})

	var results []wifprobetest.Result

	// POST /v1/organizations/federation_issuers/{id} then GET. Run before any
	// rule references the issuer, and once more afterwards (see below), so a
	// 404 from the write can be told apart from an "in use" refusal.
	probeIssuerUpdate := func(label string, trial int) {
		res := wifprobetest.Result{Endpoint: label, Trial: trial}
		want := fmt.Sprintf("tf-probe-issuer-%s-t%d", suffix, trial)
		writtenAt := wifprobetest.Write(t, &res, func() (time.Time, error) {
			updated, err := client.Beta.Organization.Federation.Issuers.Update(ctx, issuer.ID, anthropic.BetaOrganizationFederationIssuerUpdateParams{
				Name: param.NewOpt(want),
			})
			if err != nil {
				return time.Time{}, err
			}
			return updated.UpdatedAt, nil
		})
		wifprobetest.Read(t, &res, writtenAt, func() (time.Time, bool, error) {
			got, err := client.Beta.Organization.Federation.Issuers.Get(ctx, issuer.ID, anthropic.BetaOrganizationFederationIssuerGetParams{})
			if err != nil {
				return time.Time{}, false, err
			}
			return got.UpdatedAt, got.Name == want, nil
		})
		results = append(results, res)
	}
	for trial := 1; trial <= wifprobetest.Trials; trial++ {
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
		wifprobetest.Fatal(t, "create rule", err)
	}
	// Registered after the issuer and service account cleanups, so it runs
	// first (t.Cleanup is LIFO): the rule must be gone before its targets.
	t.Cleanup(func() {
		if _, err := client.Beta.Organization.Federation.Rules.Archive(ctx, rule.ID, anthropic.BetaOrganizationFederationRuleArchiveParams{}); err != nil {
			t.Logf("cleanup: archive rule %s: %s", rule.ID, err)
		}
	})

	probeIssuerUpdate("federation_issuer GET after POST update (rule live)", wifprobetest.Trials+1)

	// POST /v1/organizations/federation_rules/{id} then GET.
	for trial := 1; trial <= wifprobetest.Trials; trial++ {
		res := wifprobetest.Result{Endpoint: "federation_rule GET after POST update", Trial: trial}
		want := fmt.Sprintf("probe trial %d", trial)
		writtenAt := wifprobetest.Write(t, &res, func() (time.Time, error) {
			updated, err := client.Beta.Organization.Federation.Rules.Update(ctx, rule.ID, anthropic.BetaOrganizationFederationRuleUpdateParams{
				Description: param.NewOpt(want),
			})
			if err != nil {
				return time.Time{}, err
			}
			return updated.UpdatedAt, nil
		})
		wifprobetest.Read(t, &res, writtenAt, func() (time.Time, bool, error) {
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
	// writtenAt handed to wifprobetest.Read is zero.
	for trial := 1; trial <= wifprobetest.Trials; trial++ {
		add := wifprobetest.Result{Endpoint: "federation_rule_workspaces LIST after POST Add", Trial: trial}
		wifprobetest.Write(t, &add, func() (time.Time, error) {
			_, err := client.Beta.Organization.Federation.Rules.Workspaces.Add(ctx, rule.ID, anthropic.BetaOrganizationFederationRuleWorkspaceAddParams{
				WorkspaceID: acctest.TerraformTestsWorkspaceID,
			})
			return time.Time{}, err
		})
		wifprobetest.Read(t, &add, time.Time{}, func() (time.Time, bool, error) {
			found, err := ruleWorkspaceListed(ctx, client, rule.ID, acctest.TerraformTestsWorkspaceID)
			return time.Time{}, found, err
		})
		results = append(results, add)

		remove := wifprobetest.Result{Endpoint: "federation_rule_workspaces LIST after DELETE Remove", Trial: trial}
		wifprobetest.Write(t, &remove, func() (time.Time, error) {
			_, err := client.Beta.Organization.Federation.Rules.Workspaces.Remove(ctx, acctest.TerraformTestsWorkspaceID, anthropic.BetaOrganizationFederationRuleWorkspaceRemoveParams{
				FederationRuleID: rule.ID,
			})
			return time.Time{}, err
		})
		wifprobetest.Read(t, &remove, time.Time{}, func() (time.Time, bool, error) {
			found, err := ruleWorkspaceListed(ctx, client, rule.ID, acctest.TerraformTestsWorkspaceID)
			return time.Time{}, !found, err
		})
		results = append(results, remove)
	}

	wifprobetest.LogTable(t, results)
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
