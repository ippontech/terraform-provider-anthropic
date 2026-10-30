// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package sdktest

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strconv"
	"testing"
)

// Family is a pagination scheme of the API. A list endpoint uses one; a
// client that sends the other family's cursor gets the first page forever or
// stops after it, and the fake fails the test instead.
// Reference: https://platform.claude.com/docs/en/api/overview, "Pagination".
type Family int

const (
	// AfterID is the scheme of the Models API, the Message Batches API and
	// most Admin API lists: after_id (or before_id) in the query, data,
	// has_more, first_id and last_id in the response. The SDK reads it as
	// pagination.Page.
	// Reference: https://platform.claude.com/docs/en/api/models/list,
	// https://platform.claude.com/docs/en/api/organization/users/list.
	AfterID Family = iota + 1
	// PageToken is the scheme of most newer list endpoints: page in the
	// query, data and next_page (null on the last page) in the response.
	// The SDK reads it as pagination.PageCursor.
	// Reference: https://platform.claude.com/docs/en/api/beta/skills/list.
	PageToken
)

func (f Family) String() string {
	switch f {
	case AfterID:
		return "after_id"
	case PageToken:
		return "page"
	}
	return fmt.Sprintf("Family(%d)", int(f))
}

// Default and maximum page sizes of every list endpoint in the reference
// ("limit: Defaults to 20. Ranges from 1 to 1000").
const (
	defaultLimit = 20
	maxLimit     = 1000
)

// cursorLog counts, per cursor, how many times a route handed it out and how
// many times a client sent it back.
type cursorLog struct {
	issued map[string]int
	served map[string]int
}

// Paginate answers a list request with the page of items its cursor and limit
// select, in family's format. items must carry a string "id" and stay in the
// same order between requests; the page ends after min(limit, pageSize)
// items (pageSize 0 means the request's limit alone, which defaults to 20).
//
// It fails the test, and answers 400, when the client:
//   - sends the other family's cursor parameter (after_id or before_id to a
//     page endpoint, page or page_token to an after_id endpoint);
//   - sends a cursor this server never returned on that route, or sends one
//     more times than it was returned (a pagination loop);
//   - sends a limit outside 1..1000.
//
// before_id is not supported: no data source pages backwards.
func (s *Server) Paginate(w http.ResponseWriter, r *http.Request, family Family, pageSize int, items []map[string]any) {
	s.PaginateBy(w, r, family, pageSize, items, idOf)
}

// PaginateBy is Paginate for items that carry no "id": key returns what
// identifies an item instead (a membership's user_id, a setting's target),
// which first_id, last_id and the cursors name. Items are answered as they
// are, without an id added. key must be unique within items.
func (s *Server) PaginateBy(w http.ResponseWriter, r *http.Request, family Family, pageSize int, items []map[string]any, key func(item map[string]any) string) {
	index := map[string]int{}
	for i, it := range items {
		index[key(it)] = i
	}
	s.paginate(w, r, family, pageSize, items, key, func(k string) (int, bool) {
		i, ok := index[k]
		return i, ok
	})
}

// paginate is PaginateBy with the position of an item given by pos, which
// may still know an item deleted since its cursor was handed out
// (Collection). items must be sorted by pos.
func (s *Server) paginate(w http.ResponseWriter, r *http.Request, family Family, pageSize int, items []map[string]any,
	key func(map[string]any) string, pos func(k string) (int, bool),
) {
	route := r.Pattern
	q := r.URL.Query()
	var cursor string
	switch family {
	case AfterID:
		for _, wrong := range []string{"page", "page_token"} {
			if q.Has(wrong) {
				s.badRequest(w, "%s pages with after_id/has_more/last_id, but the client sent %s=%q", route, wrong, q.Get(wrong))
				return
			}
		}
		if q.Has("before_id") {
			s.badRequest(w, "%s: before_id is not supported by sdktest", route)
			return
		}
		cursor = q.Get("after_id")
	case PageToken:
		for _, wrong := range []string{"after_id", "before_id", "page_token"} {
			if q.Has(wrong) {
				s.badRequest(w, "%s pages with page/next_page, but the client sent %s=%q", route, wrong, q.Get(wrong))
				return
			}
		}
		cursor = q.Get("page")
	default:
		s.badRequest(w, "%s: unknown pagination family %v", route, family)
		return
	}

	limit := defaultLimit
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxLimit {
			s.badRequest(w, "%s: limit=%q is outside 1..%d", route, v, maxLimit)
			return
		}
		limit = n
	}
	if pageSize > 0 && pageSize < limit {
		limit = pageSize
	}

	start := 0
	if cursor != "" {
		id, ok := s.takeCursor(w, route, family, cursor)
		if !ok {
			return
		}
		p, ok := pos(id)
		if !ok {
			s.badRequest(w, "%s: cursor %q points at item %q, which the fake cannot place", route, cursor, id)
			return
		}
		for start < len(items) {
			if ip, _ := pos(key(items[start])); ip > p {
				break
			}
			start++
		}
	}
	end := min(start+limit, len(items))
	page := items[start:end]
	if page == nil {
		page = []map[string]any{} // the API sends "data": [], never null
	}
	more := end < len(items)

	switch family {
	case AfterID:
		var firstID, lastID any
		if len(page) > 0 {
			firstID, lastID = key(page[0]), key(page[len(page)-1])
		}
		if more {
			s.issueCursor(route, key(page[len(page)-1]))
		}
		WriteJSON(w, http.StatusOK, map[string]any{
			"data":     page,
			"has_more": more,
			"first_id": firstID,
			"last_id":  lastID,
		})
	case PageToken:
		var next any
		if more {
			token := NextPageToken(key(page[len(page)-1]))
			s.issueCursor(route, token)
			next = token
		}
		WriteJSON(w, http.StatusOK, map[string]any{
			"data":      page,
			"next_page": next,
		})
	}
}

// NextPageToken returns the next_page value the fake answers after a page
// whose last item is lastID. Clients must treat it as opaque; tests use it
// to assert the exact cursor a client sends back (AssertQueries, AssertWalks).
func NextPageToken(lastID string) string {
	return "page_" + base64.RawURLEncoding.EncodeToString([]byte(lastID))
}

func (s *Server) issueCursor(route, cursor string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	log := s.cursorLog(route)
	log.issued[cursor]++
}

// takeCursor checks a cursor the client sent and returns the item ID it
// names.
func (s *Server) takeCursor(w http.ResponseWriter, route string, family Family, cursor string) (string, bool) {
	s.mu.Lock()
	log := s.cursorLog(route)
	issued := log.issued[cursor]
	log.served[cursor]++
	served := log.served[cursor]
	s.mu.Unlock()

	switch {
	case issued == 0:
		s.badRequest(w, "%s: the client sent cursor %q, which this server never returned as %s", route, cursor, nextField(family))
		return "", false
	case served > issued:
		s.badRequest(w, "%s: the client sent cursor %q %d times, but the server returned it %d times (pagination loop)", route, cursor, served, issued)
		return "", false
	}
	if family == AfterID {
		return cursor, true
	}
	raw, err := base64.RawURLEncoding.DecodeString(cursor[len("page_"):])
	if err != nil {
		s.badRequest(w, "%s: malformed page token %q", route, cursor)
		return "", false
	}
	return string(raw), true
}

func nextField(f Family) string {
	if f == AfterID {
		return "last_id (with has_more)"
	}
	return "next_page"
}

func (s *Server) cursorLog(route string) *cursorLog {
	log := s.cursors[route]
	if log == nil {
		log = &cursorLog{issued: map[string]int{}, served: map[string]int{}}
		s.cursors[route] = log
	}
	return log
}

// AssertAllPagesRead fails the test unless the client followed every cursor
// route handed out, as many times as it handed it out: a client that stops
// after the first page, because it reads the other family's fields (has_more
// on a next_page endpoint), fails it.
func (s *Server) AssertAllPagesRead(route string) {
	s.t.Helper()
	s.mu.Lock()
	log := s.cursors[route]
	var missed []string
	if log != nil {
		for c, n := range log.issued {
			if log.served[c] < n {
				missed = append(missed, fmt.Sprintf("%q (returned %d times, followed %d)", c, n, log.served[c]))
			}
		}
	}
	s.mu.Unlock()
	if log == nil || len(log.issued) == 0 {
		s.t.Errorf("sdktest: %s never returned a next cursor; give it more items than one page holds", route)
		return
	}
	if len(missed) > 0 {
		s.t.Errorf("sdktest: the client did not follow every cursor of %s: %v", route, missed)
	}
}

func (s *Server) badRequest(w http.ResponseWriter, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	s.t.Errorf("sdktest: %s", msg)
	WriteAPIError(w, http.StatusBadRequest, "", "sdktest: "+msg)
}

func idOf(item map[string]any) string {
	id, _ := item["id"].(string)
	return id
}

// NewCursorPagedServer returns a Server whose route serves items in pages of
// pageSize, in family's format (see Paginate).
func NewCursorPagedServer(tb testing.TB, route string, family Family, pageSize int, items []map[string]any) *Server {
	tb.Helper()
	s := NewServer(tb)
	s.Handle(route, func(w http.ResponseWriter, r *http.Request) {
		s.Paginate(w, r, family, pageSize, items)
	})
	return s
}
