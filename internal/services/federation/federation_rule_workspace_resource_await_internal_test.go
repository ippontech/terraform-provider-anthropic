// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// newAwaitTestClient points a bare SDK client at srv, matching the *anthropic.Client
// awaitFederationRuleWorkspaceListed takes. Retries are off so request counts
// reflect the wait loop alone.
func newAwaitTestClient(srv *httptest.Server) *anthropic.Client {
	c := anthropic.NewClient(option.WithBaseURL(srv.URL), option.WithAuthToken("test"), option.WithoutEnvironmentDefaults(), option.WithMaxRetries(0))
	return &c
}

// newEmptyThenListedServer serves GET
// /v1/organizations/federation_rules/{rule}/workspaces with an empty list for
// the first emptyLists calls, then a single-entry list containing workspaceID
// forever after. It returns a pointer to the List counter.
func newEmptyThenListedServer(t *testing.T, ruleID, workspaceID string, emptyLists int) (*httptest.Server, *int) {
	t.Helper()

	lists := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/organizations/federation_rules/"+ruleID+"/workspaces" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		lists++
		w.Header().Set("Content-Type", "application/json")
		if lists <= emptyLists {
			if _, err := fmt.Fprint(w, `{"data":[],"next_page":null}`); err != nil {
				t.Errorf("writing response: %v", err)
			}
			return
		}
		if _, err := fmt.Fprintf(w, `{"data":[{"type":"federation_rule_workspace","federation_rule_id":%q,"workspace_id":%q,"workspace_name":"terraform-tests"}],"next_page":null}`, ruleID, workspaceID); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	return srv, &lists
}

func TestAwaitFederationRuleWorkspaceListed_pollsUntilListed(t *testing.T) {
	srv, lists := newEmptyThenListedServer(t, "fdrl_01ABC", "wrkspc_01XYZ", 3)

	found, err := awaitFederationRuleWorkspaceListed(context.Background(), newAwaitTestClient(srv), "fdrl_01ABC", "wrkspc_01XYZ", 2*time.Second, time.Millisecond)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found == nil || found.WorkspaceID != "wrkspc_01XYZ" {
		t.Fatalf("found = %+v, want the wrkspc_01XYZ entry", found)
	}
	if *lists != 4 {
		t.Errorf("List calls = %d, want 4 (three empty lists then the populated one)", *lists)
	}
}

// An entry already present must not trigger a second List.
func TestAwaitFederationRuleWorkspaceListed_returnsImmediatelyWhenListed(t *testing.T) {
	srv, lists := newEmptyThenListedServer(t, "fdrl_01ABC", "wrkspc_01XYZ", 0)

	found, err := awaitFederationRuleWorkspaceListed(context.Background(), newAwaitTestClient(srv), "fdrl_01ABC", "wrkspc_01XYZ", 2*time.Second, time.Millisecond)
	if err != nil || found == nil {
		t.Fatalf("found = %v, err = %v; want the entry and no error", found, err)
	}
	if *lists != 1 {
		t.Errorf("List calls = %d, want 1", *lists)
	}
}

// A genuinely absent entry is reported as (nil, nil) only once the whole window
// has elapsed: that is what lets Read tell out-of-band removal from a list
// that has not caught up yet.
func TestAwaitFederationRuleWorkspaceListed_reportsAbsentAtTimeout(t *testing.T) {
	srv, lists := newEmptyThenListedServer(t, "fdrl_01ABC", "wrkspc_01XYZ", 1_000_000)

	start := time.Now()
	found, err := awaitFederationRuleWorkspaceListed(context.Background(), newAwaitTestClient(srv), "fdrl_01ABC", "wrkspc_01XYZ", 120*time.Millisecond, 10*time.Millisecond)
	elapsed := time.Since(start)

	if err != nil || found != nil {
		t.Fatalf("found = %v, err = %v; want (nil, nil) at timeout", found, err)
	}
	if elapsed > time.Second {
		t.Errorf("took %s, want it to give up near the 120ms timeout", elapsed)
	}
	if *lists < 2 {
		t.Errorf("List calls = %d, want it to have retried at least once", *lists)
	}
}

// A 404 (the rule itself is gone) is returned on the first occurrence, never
// retried: Read maps it to RemoveResource and polling could not change that.
func TestAwaitFederationRuleWorkspaceListed_returnsErrorsWithoutRetrying(t *testing.T) {
	for _, status := range []int{401, 403, 404, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			lists := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				lists++
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				if _, err := fmt.Fprint(w, `{"type":"error","error":{"type":"api_error","message":"nope"}}`); err != nil {
					t.Errorf("writing response: %v", err)
				}
			}))
			t.Cleanup(srv.Close)

			found, err := awaitFederationRuleWorkspaceListed(context.Background(), newAwaitTestClient(srv), "fdrl_01ABC", "wrkspc_01XYZ", 10*time.Second, 50*time.Millisecond)

			var apierr *anthropic.Error
			if !errors.As(err, &apierr) || apierr.StatusCode != status {
				t.Fatalf("err = %v, want an *anthropic.Error with status %d", err, status)
			}
			if found != nil {
				t.Errorf("found = %+v, want nil alongside the error", found)
			}
			if lists != 1 {
				t.Errorf("List calls = %d, want exactly 1 (errors are not retried)", lists)
			}
		})
	}
}

// A cancelled context must abort the wait promptly and surface ctx.Err().
func TestAwaitFederationRuleWorkspaceListed_honoursContextCancellation(t *testing.T) {
	srv, _ := newEmptyThenListedServer(t, "fdrl_01ABC", "wrkspc_01XYZ", 1_000_000)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	_, err := awaitFederationRuleWorkspaceListed(ctx, newAwaitTestClient(srv), "fdrl_01ABC", "wrkspc_01XYZ", 10*time.Second, 50*time.Millisecond)

	if time.Since(start) > time.Second {
		t.Errorf("took %s, want an immediate return on a cancelled context", time.Since(start))
	}
	if err == nil {
		t.Error("err = nil, want a context error")
	}
}
