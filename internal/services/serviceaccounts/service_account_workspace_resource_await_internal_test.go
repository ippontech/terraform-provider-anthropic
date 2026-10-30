// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package serviceaccounts

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/ippontech/terraform-provider-anthropic/internal/oauthtest"
)

// newStaleRoleListServer serves GET
// /v1/organizations/service_accounts/svac_01ABC/workspaces with the membership
// in wrkspc_01XYZ at workspace_developer for the first staleLists calls, then
// at workspace_admin forever after. It returns a pointer to the List counter.
func newStaleRoleListServer(t *testing.T, staleLists int) (*httptest.Server, *int) {
	t.Helper()
	const (
		serviceAccountID = "svac_01ABC"
		workspaceID      = "wrkspc_01XYZ"
		oldRole          = "workspace_developer"
		newRole          = "workspace_admin"
	)

	lists := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/v1/organizations/service_accounts/"+serviceAccountID+"/workspaces" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
			return
		}
		lists++
		role := newRole
		if lists <= staleLists {
			role = oldRole
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := fmt.Fprintf(w, `{"data": [%s], "next_page": null}`, serviceAccountWorkspaceMemberJSON(serviceAccountID, workspaceID, role)); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)

	return srv, &lists
}

// A list still showing the pre-update role right after a successful write
// would make the next plan propose the role change again.
func TestAwaitServiceAccountWorkspaceRoleVisible_pollsUntilVisible(t *testing.T) {
	srv, lists := newStaleRoleListServer(t, 3)
	client := oauthtest.NewClient(t, srv, option.WithMaxRetries(0))

	awaitServiceAccountWorkspaceRoleVisible(context.Background(), client, "svac_01ABC", "wrkspc_01XYZ", "workspace_admin", 2*time.Second, time.Millisecond)

	if *lists != 4 {
		t.Errorf("List calls = %d, want 4 (three stale lists then the fresh one)", *lists)
	}
}

// A list that already reflects the write must not be polled a second time.
func TestAwaitServiceAccountWorkspaceRoleVisible_returnsImmediatelyWhenVisible(t *testing.T) {
	srv, lists := newStaleRoleListServer(t, 0)
	client := oauthtest.NewClient(t, srv, option.WithMaxRetries(0))

	awaitServiceAccountWorkspaceRoleVisible(context.Background(), client, "svac_01ABC", "wrkspc_01XYZ", "workspace_admin", 2*time.Second, time.Millisecond)

	if *lists != 1 {
		t.Errorf("List calls = %d, want 1", *lists)
	}
}

// The wait is best-effort: a role that never shows up must return at the
// timeout rather than hang or surface an error, since the write succeeded.
func TestAwaitServiceAccountWorkspaceRoleVisible_givesUpAtTimeout(t *testing.T) {
	srv, lists := newStaleRoleListServer(t, 1_000_000)
	client := oauthtest.NewClient(t, srv, option.WithMaxRetries(0))

	start := time.Now()
	awaitServiceAccountWorkspaceRoleVisible(context.Background(), client, "svac_01ABC", "wrkspc_01XYZ", "workspace_admin", 120*time.Millisecond, 10*time.Millisecond)
	elapsed := time.Since(start)

	if elapsed > time.Second {
		t.Errorf("took %s, want it to give up near the 120ms timeout", elapsed)
	}
	if *lists < 2 {
		t.Errorf("List calls = %d, want it to have retried at least once", *lists)
	}
}

// A membership missing from the list cannot be a stale read (it existed
// before the write), so the wait stops at once and leaves the drift to Read.
func TestAwaitServiceAccountWorkspaceRoleVisible_stopsWhenMembershipGone(t *testing.T) {
	lists := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		lists++
		w.Header().Set("Content-Type", "application/json")
		if _, err := fmt.Fprint(w, `{"data": [], "next_page": null}`); err != nil {
			t.Errorf("writing response: %v", err)
		}
	}))
	t.Cleanup(srv.Close)
	client := oauthtest.NewClient(t, srv, option.WithMaxRetries(0))

	start := time.Now()
	awaitServiceAccountWorkspaceRoleVisible(context.Background(), client, "svac_01ABC", "wrkspc_01XYZ", "workspace_admin", 10*time.Second, 50*time.Millisecond)

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("took %s, want an immediate return", elapsed)
	}
	if lists != 1 {
		t.Errorf("List calls = %d, want 1", lists)
	}
}

// A service account archived out-of-band (404) or a token that lost access
// (401/403) never converges: the wait must stop on the first such answer.
func TestAwaitServiceAccountWorkspaceRoleVisible_stopsOnTerminalReadError(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			srv, lists := newFailingServiceAccountServer(t, status)
			client := oauthtest.NewClient(t, srv, option.WithMaxRetries(0))

			start := time.Now()
			awaitServiceAccountWorkspaceRoleVisible(context.Background(), client, "svac_01ABC", "wrkspc_01XYZ", "workspace_admin", 10*time.Second, 50*time.Millisecond)

			if elapsed := time.Since(start); elapsed > time.Second {
				t.Errorf("took %s, want an immediate return on a terminal read error", elapsed)
			}
			if *lists != 1 {
				t.Errorf("List calls = %d, want 1 (no retry on a terminal status)", *lists)
			}
		})
	}
}

// A cancelled context must abort the wait promptly.
func TestAwaitServiceAccountWorkspaceRoleVisible_honoursContextCancellation(t *testing.T) {
	srv, _ := newStaleRoleListServer(t, 1_000_000)
	client := oauthtest.NewClient(t, srv, option.WithMaxRetries(0))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	start := time.Now()
	awaitServiceAccountWorkspaceRoleVisible(ctx, client, "svac_01ABC", "wrkspc_01XYZ", "workspace_admin", 10*time.Second, 50*time.Millisecond)

	if elapsed := time.Since(start); elapsed > time.Second {
		t.Errorf("took %s, want an immediate return on a cancelled context", elapsed)
	}
}

// A list call that is slow or throttled (the SDK retries and honors
// retry-after) is cut off at the timeout: the wait does not run past it by a
// whole poll.
func TestAwaitServiceAccountWorkspaceRoleVisible_cutsASlowListAtTimeout(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(3 * time.Second):
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"data": [%s], "next_page": null}`, serviceAccountWorkspaceMemberJSON("svac_01ABC", "wrkspc_01XYZ", "workspace_developer"))
	}))
	t.Cleanup(srv.Close)
	client := oauthtest.NewClient(t, srv, option.WithMaxRetries(0))

	start := time.Now()
	awaitServiceAccountWorkspaceRoleVisible(context.Background(), client, "svac_01ABC", "wrkspc_01XYZ", "workspace_admin", 150*time.Millisecond, 10*time.Millisecond)
	if elapsed := time.Since(start); elapsed > 1500*time.Millisecond {
		t.Errorf("wait took %v, want it cut off near the 150ms timeout", elapsed)
	}
}
