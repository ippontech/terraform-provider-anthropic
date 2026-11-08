// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package sdkpage

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/pagination"
	"github.com/ippontech/terraform-provider-anthropic/internal/oauthtest"
)

type vault = anthropic.BetaManagedAgentsVault

// listServer answers GET /v1/vaults with the body pageFor returns for the
// `page` query value ("" for the first request), and records every value.
type listServer struct {
	mu    sync.Mutex
	pages []string
}

func (s *listServer) start(t *testing.T, pageFor func(page string) (status int, body string)) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := r.URL.Query().Get("page")
		s.mu.Lock()
		s.pages = append(s.pages, page)
		s.mu.Unlock()
		status, body := pageFor(page)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv
}

func (s *listServer) requested() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.pages...)
}

func pageJSON(next string, ids ...string) string {
	items := make([]string, len(ids))
	for i, id := range ids {
		items[i] = `{"id":"` + id + `","type":"vault"}`
	}
	cursor := "null"
	if next != "" {
		cursor = `"` + next + `"`
	}
	return `{"data":[` + strings.Join(items, ",") + `],"next_page":` + cursor + `}`
}

// firstPage returns the first page of the vault list from srv, as
// sdkpage.Cursor receives it from a List method.
func firstPage(t *testing.T, srv *httptest.Server) (*pagination.PageCursor[vault], error) {
	t.Helper()
	client := oauthtest.NewSDKClient(t, srv, option.WithMaxRetries(0))
	return client.Beta.Vaults.List(context.Background(), anthropic.BetaVaultListParams{})
}

func collect(ids *[]string) func(vault) error {
	return func(v vault) error {
		*ids = append(*ids, v.ID)
		return nil
	}
}

func TestCursor_walksEveryPage(t *testing.T) {
	var s listServer
	srv := s.start(t, func(page string) (int, string) {
		switch page {
		case "":
			return http.StatusOK, pageJSON("c2", "v1", "v2")
		case "c2":
			return http.StatusOK, pageJSON("c3", "v3")
		default:
			return http.StatusOK, pageJSON("", "v4")
		}
	})

	var ids []string
	first, err := firstPage(t, srv)
	if err := Cursor(first, err, collect(&ids)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.Join(ids, ","); got != "v1,v2,v3,v4" {
		t.Errorf("visited %s, want v1,v2,v3,v4 in API order", got)
	}
	if got := strings.Join(s.requested(), ","); got != ",c2,c3" {
		t.Errorf("requested pages %q, want \",c2,c3\"", got)
	}
}

// An empty page with a cursor does not end the walk: only an empty cursor
// does, as in the SDK's auto-pager since v1.77.0.
func TestCursor_continuesPastEmptyPage(t *testing.T) {
	var s listServer
	srv := s.start(t, func(page string) (int, string) {
		switch page {
		case "":
			return http.StatusOK, pageJSON("c2", "v1")
		case "c2":
			return http.StatusOK, pageJSON("c3")
		default:
			return http.StatusOK, pageJSON("", "v3")
		}
	})

	var ids []string
	first, err := firstPage(t, srv)
	if err := Cursor(first, err, collect(&ids)); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.Join(ids, ","); got != "v1,v3" {
		t.Errorf("visited %s, want v1,v3 across the empty page", got)
	}
	if got := strings.Join(s.requested(), ","); got != ",c2,c3" {
		t.Errorf("requested pages %q, want \",c2,c3\"", got)
	}
}

// Empty pages that keep returning one cursor still end in an error rather
// than a loop.
func TestCursor_emptyPagesWithRepeatedCursorIsAnError(t *testing.T) {
	var s listServer
	srv := s.start(t, func(string) (int, string) {
		return http.StatusOK, pageJSON("c1")
	})

	first, err := firstPage(t, srv)
	err = Cursor(first, err, func(vault) error { return nil })
	if err == nil || !strings.Contains(err.Error(), `"c1" again`) {
		t.Fatalf("error = %v, want one naming the repeated cursor c1", err)
	}
	if got := len(s.requested()); got != 2 {
		t.Errorf("%d requests, want 2", got)
	}
}

func TestCursor_repeatedCursorIsAnError(t *testing.T) {
	var s listServer
	srv := s.start(t, func(string) (int, string) {
		return http.StatusOK, pageJSON("c1", "v1")
	})

	first, err := firstPage(t, srv)
	err = Cursor(first, err, func(vault) error { return nil })
	if err == nil || !strings.Contains(err.Error(), `"c1" again`) {
		t.Fatalf("error = %v, want one naming the repeated cursor c1", err)
	}
	if got := len(s.requested()); got != 2 {
		t.Errorf("%d requests, want 2: the cursor is refused the second time the API returns it", got)
	}
}

func TestCursor_cursorCycleIsAnError(t *testing.T) {
	var s listServer
	srv := s.start(t, func(page string) (int, string) {
		if page == "c1" {
			return http.StatusOK, pageJSON("c2", "v2")
		}
		return http.StatusOK, pageJSON("c1", "v1")
	})

	first, err := firstPage(t, srv)
	err = Cursor(first, err, func(vault) error { return nil })
	if err == nil || !strings.Contains(err.Error(), `"c1" again`) {
		t.Fatalf("error = %v, want one naming the cursor c1 that came back", err)
	}
	if got := strings.Join(s.requested(), ","); got != ",c1,c2" {
		t.Errorf("requested pages %q, want \",c1,c2\"", got)
	}
}

func TestCursorUpTo_stopsAtThePageBound(t *testing.T) {
	var s listServer
	n := 0
	srv := s.start(t, func(string) (int, string) {
		n++
		return http.StatusOK, pageJSON("c"+strconv.Itoa(n), "v"+strconv.Itoa(n))
	})

	first, err := firstPage(t, srv)
	err = cursorUpTo(first, err, 3, func(vault) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "after 3 pages") {
		t.Fatalf("error = %v, want the page bound of 3", err)
	}
	if got := len(s.requested()); got != 3 {
		t.Errorf("%d requests, want 3", got)
	}
}

func TestCursor_returnsTheListError(t *testing.T) {
	var s listServer
	srv := s.start(t, func(string) (int, string) {
		return http.StatusForbidden, `{"type":"error","error":{"type":"permission_error","message":"forbidden"}}`
	})

	visited := false
	first, err := firstPage(t, srv)
	err = Cursor(first, err, func(vault) error { visited = true; return nil })
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("error = %v, want the API's 403", err)
	}
	if visited {
		t.Error("visit called although the first request failed")
	}
}

func TestCursor_returnsTheNextPageError(t *testing.T) {
	var s listServer
	srv := s.start(t, func(page string) (int, string) {
		if page == "" {
			return http.StatusOK, pageJSON("c2", "v1")
		}
		return http.StatusForbidden, `{"type":"error","error":{"type":"permission_error","message":"forbidden"}}`
	})

	var ids []string
	first, err := firstPage(t, srv)
	err = Cursor(first, err, collect(&ids))
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("error = %v, want the second page's 403", err)
	}
	if len(ids) != 1 {
		t.Errorf("visited %v, want the first page's item only", ids)
	}
}

func TestCursor_visitErrorStopsTheWalk(t *testing.T) {
	var s listServer
	srv := s.start(t, func(string) (int, string) {
		return http.StatusOK, pageJSON("c2", "v1", "v2")
	})

	stop := errors.New("stop")
	calls := 0
	first, err := firstPage(t, srv)
	err = Cursor(first, err, func(vault) error { calls++; return stop })
	if !errors.Is(err, stop) {
		t.Fatalf("error = %v, want visit's error", err)
	}
	if calls != 1 || len(s.requested()) != 1 {
		t.Errorf("%d visits over %d requests, want 1 over 1", calls, len(s.requested()))
	}
}

func TestCursorUpTo_nilPage(t *testing.T) {
	if err := cursorUpTo[vault](nil, nil, 1, func(vault) error { return errors.New("not called") }); err != nil {
		t.Fatalf("error = %v, want nil for a nil page", err)
	}
}
