// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package sdktest

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
)

func items(n int) []map[string]any {
	out := make([]map[string]any, n)
	for i := range out {
		out[i] = map[string]any{"id": fmt.Sprintf("item_%d", i+1)}
	}
	return out
}

// TestPaginateAfterIDWithTheSDK walks five items, two per page, with the
// SDK's own auto-pager for after_id lists (pagination.Page).
func TestPaginateAfterIDWithTheSDK(t *testing.T) {
	rec := newRecorderTB(t)
	s := NewCursorPagedServer(rec, "GET /v1/models", AfterID, 2, items(5))
	c := NewAPIKeyClient(t, s.Server)

	pager := c.Models.ListAutoPaging(context.Background(), anthropic.ModelListParams{Limit: anthropic.Int(1000)})
	var got []string
	for pager.Next() {
		got = append(got, pager.Current().ID)
	}
	if err := pager.Err(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "item_1,item_2,item_3,item_4,item_5" {
		t.Errorf("listed %q", got)
	}
	s.AssertQueries("GET /v1/models", "limit=1000", "after_id=item_2&limit=1000", "after_id=item_4&limit=1000")
	s.AssertAllPagesRead("GET /v1/models")
	expectNoFailure(t, rec)
	if AfterID.String() != "after_id" || PageToken.String() != "page" || Family(7).String() != "Family(7)" {
		t.Error("Family.String")
	}
}

// TestPaginatePageTokenWithTheSDK walks the same items with the SDK's
// auto-pager for page/next_page lists (pagination.PageCursor).
func TestPaginatePageTokenWithTheSDK(t *testing.T) {
	rec := newRecorderTB(t)
	s := NewCursorPagedServer(rec, "GET /v1/skills", PageToken, 2, items(5))
	c := NewAPIKeyClient(t, s.Server)

	pager := c.Beta.Skills.ListAutoPaging(context.Background(), anthropic.BetaSkillListParams{})
	var got []string
	for pager.Next() {
		got = append(got, pager.Current().ID)
	}
	if err := pager.Err(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "item_1,item_2,item_3,item_4,item_5" {
		t.Errorf("listed %q", got)
	}
	s.AssertQueries("GET /v1/skills",
		"beta=true",
		"beta=true&page="+NextPageToken("item_2"),
		"beta=true&page="+NextPageToken("item_4"))
	s.AssertAllPagesRead("GET /v1/skills")
	expectNoFailure(t, rec)
}

// get sends a raw GET and returns the status and body.
func get(t *testing.T, s *Server, path string) (int, string) {
	t.Helper()
	resp, err := http.Get(s.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

// TestPaginateCatchesBrokenClients: every client mistake the doc lists fails
// the test and gets a 400 the client will not retry.
func TestPaginateCatchesBrokenClients(t *testing.T) {
	cases := []struct {
		name, family, query, want string
	}{
		{"page cursor to an after_id list", "after", "?page=x", "pages with after_id/has_more/last_id, but the client sent page="},
		{"page_token to an after_id list", "after", "?page_token=x", "but the client sent page_token="},
		{"backwards", "after", "?before_id=item_3", "before_id is not supported"},
		{"after_id to a page list", "page", "?after_id=item_2", "pages with page/next_page, but the client sent after_id="},
		{"invented after_id", "after", "?after_id=item_1", `cursor "item_1", which this server never returned as last_id`},
		{"invented page", "page", "?page=page_eA", "never returned as next_page"},
		{"limit too large", "after", "?limit=1001", "outside 1..1000"},
		{"limit not a number", "page", "?limit=ten", "outside 1..1000"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := newRecorderTB(t)
			family := AfterID
			if tc.family == "page" {
				family = PageToken
			}
			s := NewCursorPagedServer(rec, "GET /v1/list", family, 2, items(5))
			status, body := get(t, s, "/v1/list"+tc.query)
			if status != http.StatusBadRequest || !strings.Contains(body, "invalid_request_error") {
				t.Errorf("answered %d %s, want a 400 invalid_request_error", status, body)
			}
			expectFailure(t, rec, tc.want)
		})
	}
	t.Run("unknown family", func(t *testing.T) {
		rec := newRecorderTB(t)
		s := NewCursorPagedServer(rec, "GET /v1/list", Family(0), 2, items(5))
		get(t, s, "/v1/list")
		expectFailure(t, rec, "unknown pagination family")
	})
}

// TestPaginateCatchesALoop: a client that sends a cursor back more times
// than it received it is looping.
func TestPaginateCatchesALoop(t *testing.T) {
	rec := newRecorderTB(t)
	s := NewCursorPagedServer(rec, "GET /v1/list", AfterID, 2, items(5))
	get(t, s, "/v1/list")
	if status, _ := get(t, s, "/v1/list?after_id=item_2"); status != http.StatusOK {
		t.Fatalf("first use of the cursor answered %d", status)
	}
	expectNoFailure(t, rec)
	if status, _ := get(t, s, "/v1/list?after_id=item_2"); status != http.StatusBadRequest {
		t.Errorf("second use of the cursor answered %d", status)
	}
	expectFailure(t, rec, `sent cursor "item_2" 2 times, but the server returned it 1 times`)
}

// TestAssertAllPagesRead catches a client that stops after the first page,
// as a client reading has_more on a next_page list does (B7 in the
// robustness audit).
func TestAssertAllPagesRead(t *testing.T) {
	rec := newRecorderTB(t)
	s := NewCursorPagedServer(rec, "GET /v1/list", PageToken, 2, items(5))
	_, body := get(t, s, "/v1/list")
	if !strings.Contains(body, `"next_page":"`+NextPageToken("item_2")+`"`) || strings.Contains(body, "has_more") {
		t.Errorf("first page %s", body)
	}
	s.AssertAllPagesRead("GET /v1/list")
	expectFailure(t, rec, "did not follow every cursor")

	s.AssertAllPagesRead("GET /v1/other")
	expectFailure(t, rec, "never returned a next cursor")
}

func TestPaginatePageShapes(t *testing.T) {
	s := NewServer(t)
	s.Handle("GET /v1/after", func(w http.ResponseWriter, r *http.Request) { s.Paginate(w, r, AfterID, 0, items(3)) })
	s.Handle("GET /v1/empty", func(w http.ResponseWriter, r *http.Request) { s.Paginate(w, r, AfterID, 0, nil) })
	s.Handle("GET /v1/page", func(w http.ResponseWriter, r *http.Request) { s.Paginate(w, r, PageToken, 0, items(3)) })
	for path, want := range map[string]string{
		// pageSize 0: the request's limit, else the documented default of 20.
		"/v1/after":         `{"data":[{"id":"item_1"},{"id":"item_2"},{"id":"item_3"}],"first_id":"item_1","has_more":false,"last_id":"item_3"}`,
		"/v1/after?limit=1": `{"data":[{"id":"item_1"}],"first_id":"item_1","has_more":true,"last_id":"item_1"}`,
		"/v1/empty":         `{"data":[],"first_id":null,"has_more":false,"last_id":null}`,
		"/v1/page":          `{"data":[{"id":"item_1"},{"id":"item_2"},{"id":"item_3"}],"next_page":null}`,
	} {
		_, body := get(t, s, path)
		if diff := JSONDiff(body, want); diff != "" {
			t.Errorf("%s:\n%s", path, diff)
		}
	}
}

// TestPaginateMalformedToken: a page token that went through issue but does
// not decode cannot happen with the fake's own tokens; the guard still
// answers 400 rather than panicking.
func TestPaginateMalformedToken(t *testing.T) {
	rec := newRecorderTB(t)
	s := NewCursorPagedServer(rec, "GET /v1/list", PageToken, 2, items(5))
	s.issueCursor("GET /v1/list", "page_!!")
	if status, _ := get(t, s, "/v1/list?page=page_!!"); status != http.StatusBadRequest {
		t.Errorf("answered %d", status)
	}
	expectFailure(t, rec, "malformed page token")

	s.issueCursor("GET /v1/list", NextPageToken("item_9"))
	if status, _ := get(t, s, "/v1/list?page="+NextPageToken("item_9")); status != http.StatusBadRequest {
		t.Errorf("answered %d", status)
	}
	expectFailure(t, rec, `points at item "item_9", which the fake cannot place`)
}
