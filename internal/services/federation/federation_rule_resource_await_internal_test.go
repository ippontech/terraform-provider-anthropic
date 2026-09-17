// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/ippontech/terraform-provider-anthropic/internal/oauthtest"
)

// newStaleThenFreshRuleServer serves GET /v1/organizations/federation_rules/{id}
// with updated_at = stale for the first staleReads calls, then fresh forever
// after. It returns a pointer to the Get counter.
func newStaleThenFreshRuleServer(t *testing.T, id string, stale, fresh time.Time, staleReads int) (*httptest.Server, *int) {
	t.Helper()

	gets := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/organizations/federation_rules/"+id {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		gets++
		ts := fresh
		if gets <= staleReads {
			ts = stale
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := fmt.Fprintf(w, `{"id":%q,"type":"federation_rule","name":"r","updated_at":%q}`, id, ts.Format(time.RFC3339Nano)); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	return srv, &gets
}

// newFailingRuleServer answers every Get with status, so the poll can never
// converge.
func newFailingRuleServer(t *testing.T, status int) (*httptest.Server, *int) {
	t.Helper()

	gets := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		gets++
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		if _, err := fmt.Fprint(w, `{"type":"error","error":{"type":"api_error","message":"nope"}}`); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	return srv, &gets
}

func TestAwaitFederationRuleUpdateVisible_pollsUntilFresh(t *testing.T) {
	before := time.Date(2026, 9, 8, 11, 0, 0, 0, time.UTC)
	after := before.Add(time.Second)

	srv, gets := newStaleThenFreshRuleServer(t, "fdrl_01ABC", before, after, 3)

	awaitFederationRuleUpdateVisible(context.Background(), oauthtest.NewClient(t, srv), "fdrl_01ABC", after, 2*time.Second, time.Millisecond)

	if *gets != 4 {
		t.Errorf("Get calls = %d, want 4 (three stale reads then the fresh one)", *gets)
	}
}

// A Get that already reflects the write must not be polled a second time.
func TestAwaitFederationRuleUpdateVisible_returnsImmediatelyWhenFresh(t *testing.T) {
	before := time.Date(2026, 9, 8, 11, 0, 0, 0, time.UTC)
	after := before.Add(time.Second)

	srv, gets := newStaleThenFreshRuleServer(t, "fdrl_01ABC", before, after, 0)

	awaitFederationRuleUpdateVisible(context.Background(), oauthtest.NewClient(t, srv), "fdrl_01ABC", after, 2*time.Second, time.Millisecond)

	if *gets != 1 {
		t.Errorf("Get calls = %d, want 1", *gets)
	}
}

// An updated_at equal to the write timestamp counts as visible: the comparison
// must not require a strictly newer value, or the loop would always time out.
func TestAwaitFederationRuleUpdateVisible_equalTimestampCountsAsVisible(t *testing.T) {
	writtenAt := time.Date(2026, 9, 8, 11, 0, 0, 0, time.UTC)

	srv, gets := newStaleThenFreshRuleServer(t, "fdrl_01ABC", writtenAt, writtenAt, 0)

	awaitFederationRuleUpdateVisible(context.Background(), oauthtest.NewClient(t, srv), "fdrl_01ABC", writtenAt, 200*time.Millisecond, time.Millisecond)

	if *gets != 1 {
		t.Errorf("Get calls = %d, want 1", *gets)
	}
}

// The wait is best-effort: a rule that never converges must return at the
// timeout rather than hang or surface an error, because the write itself
// already succeeded.
func TestAwaitFederationRuleUpdateVisible_givesUpAtTimeout(t *testing.T) {
	before := time.Date(2026, 9, 8, 11, 0, 0, 0, time.UTC)
	after := before.Add(time.Second)

	srv, gets := newStaleThenFreshRuleServer(t, "fdrl_01ABC", before, after, 1_000_000)

	start := time.Now()
	awaitFederationRuleUpdateVisible(context.Background(), oauthtest.NewClient(t, srv), "fdrl_01ABC", after, 120*time.Millisecond, 10*time.Millisecond)

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("took %s, want it to give up near the 120ms timeout", elapsed)
	}
	if *gets < 2 {
		t.Errorf("Get calls = %d, want it to have retried at least once", *gets)
	}
}

// A cancelled context must abort the wait promptly.
func TestAwaitFederationRuleUpdateVisible_honoursContextCancellation(t *testing.T) {
	before := time.Date(2026, 9, 8, 11, 0, 0, 0, time.UTC)
	after := before.Add(time.Second)

	srv, _ := newStaleThenFreshRuleServer(t, "fdrl_01ABC", before, after, 1_000_000)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	awaitFederationRuleUpdateVisible(ctx, oauthtest.NewClient(t, srv), "fdrl_01ABC", after, 10*time.Second, 50*time.Millisecond)

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("took %s, want an immediate return on a cancelled context", elapsed)
	}
}

// A rule archived out-of-band, or a token that lost access to it, answers
// every Get with a terminal status. Polling it to the deadline would stall the
// apply for seconds on a read that can never converge, so the wait must bail
// out on the first such answer.
func TestAwaitFederationRuleUpdateVisible_stopsOnTerminalReadError(t *testing.T) {
	for _, status := range []int{401, 403, 404} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv, gets := newFailingRuleServer(t, status)
			writtenAt := time.Date(2026, 9, 8, 11, 0, 0, 0, time.UTC)

			start := time.Now()
			awaitFederationRuleUpdateVisible(context.Background(), oauthtest.NewClient(t, srv), "fdrl_01ABC", writtenAt, 10*time.Second, 50*time.Millisecond)

			if elapsed := time.Since(start); elapsed > time.Second {
				t.Errorf("took %s, want an immediate return on a %d", elapsed, status)
			}
			if *gets != 1 {
				t.Errorf("Get calls = %d, want exactly 1 (no retry on a terminal status)", *gets)
			}
		})
	}
}

// Any other read failure says nothing about visibility, so the poll keeps
// going until the deadline. The SDK client's own retries are disabled so the
// counter reflects the wait loop alone.
func TestAwaitFederationRuleUpdateVisible_keepsPollingOnOtherReadErrors(t *testing.T) {
	srv, gets := newFailingRuleServer(t, http.StatusInternalServerError)
	client := oauthtest.NewClient(t, srv, option.WithMaxRetries(0))
	writtenAt := time.Date(2026, 9, 8, 11, 0, 0, 0, time.UTC)

	awaitFederationRuleUpdateVisible(context.Background(), client, "fdrl_01ABC", writtenAt, 100*time.Millisecond, 10*time.Millisecond)

	if *gets < 2 {
		t.Errorf("Get calls = %d, want the poll to retry a non-terminal error", *gets)
	}
}

// --- completeFederationRuleIssuerName ---

const federationRuleCreateResponseNoIssuerName = `{
	"id": "fdrl_01ABC",
	"type": "federation_rule",
	"name": "gha-deploy",
	"issuer_id": "fdis_01XYZ",
	"issuer_name": "",
	"oauth_scope": "workspace:developer",
	"applies_to_all_workspaces": true,
	"token_lifetime_seconds": 3600,
	"match": {"subject_prefix": "repo:my-org/*"},
	"target": {"service_account_id": "svac_01SVC", "type": "service_account"},
	"updated_at": "2024-01-15T10:00:00Z"
}`

// TestCompleteFederationRuleIssuerName_ReadsBackWhenEmpty is the #243 case:
// the create payload carries no issuer_name, the GET does.
func TestCompleteFederationRuleIssuerName_ReadsBackWhenEmpty(t *testing.T) {
	gets := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/organizations/federation_rules/fdrl_01ABC" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		gets++
		w.Header().Set("Content-Type", "application/json")
		if _, err := fmt.Fprint(w, `{"id":"fdrl_01ABC","type":"federation_rule","name":"gha-deploy","issuer_id":"fdis_01XYZ","issuer_name":"GitHub Actions","updated_at":"2024-01-15T10:00:00Z"}`); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	var created anthropic.BetaFederationRule
	if err := created.UnmarshalJSON([]byte(federationRuleCreateResponseNoIssuerName)); err != nil {
		t.Fatalf("unmarshal fixture: %v", err)
	}

	got := completeFederationRuleIssuerName(context.Background(), oauthtest.NewClient(t, srv), &created)

	if gets != 1 {
		t.Errorf("gets = %d, want 1", gets)
	}
	if got.IssuerName != "GitHub Actions" {
		t.Errorf("IssuerName = %q, want %q", got.IssuerName, "GitHub Actions")
	}
	if got.ID != "fdrl_01ABC" || got.Name != "gha-deploy" {
		t.Errorf("read-back rule = %+v, want id/name preserved", got)
	}
}

// TestCompleteFederationRuleIssuerName_SkipsReadWhenPresent: no extra
// request when the create response already carries the name.
func TestCompleteFederationRuleIssuerName_SkipsReadWhenPresent(t *testing.T) {
	srv, gets := newFailingRuleServer(t, http.StatusInternalServerError)

	created := &anthropic.BetaFederationRule{ID: "fdrl_01ABC", IssuerName: "GitHub Actions"}
	got := completeFederationRuleIssuerName(context.Background(), oauthtest.NewClient(t, srv), created)

	if *gets != 0 {
		t.Errorf("gets = %d, want 0", *gets)
	}
	if got != created {
		t.Errorf("expected the create response to be returned unchanged")
	}
}

// TestCompleteFederationRuleIssuerName_KeepsCreateResponseOnError: the
// read-back is best-effort, a failing GET must not lose the created rule.
func TestCompleteFederationRuleIssuerName_KeepsCreateResponseOnError(t *testing.T) {
	srv, gets := newFailingRuleServer(t, http.StatusInternalServerError)

	created := &anthropic.BetaFederationRule{ID: "fdrl_01ABC"}
	got := completeFederationRuleIssuerName(context.Background(), oauthtest.NewClient(t, srv, option.WithMaxRetries(0)), created)

	if *gets != 1 {
		t.Errorf("gets = %d, want 1", *gets)
	}
	if got != created {
		t.Errorf("expected the create response to be returned on read-back failure")
	}
}
