// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package sdktest

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

// newThingsAPI serves a CRUD API of "things" and of their "parts", a child
// collection addressed under its parent.
func newThingsAPI(tb testing.TB, mode UpdateMode) (*Server, *Collection, *Collection) {
	tb.Helper()
	s := NewServer(tb)
	things := s.NewCollection("thing_")
	things.HandleCreate("POST /v1/things")
	things.HandleGet("GET /v1/things/{id}")
	things.HandleUpdate("POST /v1/things/{id}", mode)
	things.HandleDelete("DELETE /v1/things/{id}", "thing_deleted")
	things.HandleArchive("POST /v1/things/{id}/archive")
	things.HandleList("GET /v1/things", PageToken, 2, func(r *http.Request, o map[string]any) bool {
		return r.URL.Query().Get("color") == "" || o["color"] == r.URL.Query().Get("color")
	})
	parts := s.NewCollection("part_").KeyedBy("name")
	parts.HandleCreate("POST /v1/things/{thing_id}/parts")
	parts.HandleGet("GET /v1/things/{thing_id}/parts/{name}")
	parts.HandleList("GET /v1/things/{thing_id}/parts", AfterID, 0, nil)
	return s, things, parts
}

func TestCollectionCRUDOverHTTP(t *testing.T) {
	s, things, _ := newThingsAPI(t, Patch)
	c := NewAPIKeyClient(t, s.Server, option.WithMaxRetries(0))
	ctx := context.Background()

	var created map[string]any
	if err := c.Post(ctx, "/v1/things", map[string]any{"name": "a", "metadata": map[string]any{"k1": "v1", "k2": "v2"}}, &created); err != nil {
		t.Fatal(err)
	}
	want := `{"id":"thing_000000000000000000000001","name":"a","metadata":{"k1":"v1","k2":"v2"},
		"created_at":"2026-01-01T00:00:01Z","updated_at":"2026-01-01T00:00:01Z"}`
	if diff := JSONDiff(render(created), want); diff != "" {
		t.Errorf("created:\n%s", diff)
	}
	id := created["id"].(string)

	var updated map[string]any
	if err := c.Post(ctx, "/v1/things/"+id, map[string]any{"metadata": map[string]any{"k1": nil, "k3": "v3"}, "name": nil}, &updated); err != nil {
		t.Fatal(err)
	}
	want = `{"id":"thing_000000000000000000000001","name":null,"metadata":{"k2":"v2","k3":"v3"},
		"created_at":"2026-01-01T00:00:01Z","updated_at":"2026-01-01T00:00:02Z"}`
	if diff := JSONDiff(render(updated), want); diff != "" {
		t.Errorf("patched:\n%s", diff)
	}

	var archived map[string]any
	if err := c.Post(ctx, "/v1/things/"+id+"/archive", nil, &archived); err != nil {
		t.Fatal(err)
	}
	if archived["archived_at"] != "2026-01-01T00:00:03Z" {
		t.Errorf("archived %v", archived)
	}
	// Archiving again keeps the first timestamp.
	if again, _ := things.Archive(id); again["archived_at"] != "2026-01-01T00:00:03Z" {
		t.Errorf("archived twice: %v", again)
	}

	var got map[string]any
	if err := c.Get(ctx, "/v1/things/"+id, nil, &got); err != nil || got["archived_at"] == nil {
		t.Errorf("get: %v %v", got, err)
	}
	var deleted map[string]any
	if err := c.Delete(ctx, "/v1/things/"+id, nil, &deleted); err != nil {
		t.Fatal(err)
	}
	if diff := JSONDiff(render(deleted), `{"id":"thing_000000000000000000000001","type":"thing_deleted"}`); diff != "" {
		t.Error(diff)
	}
	for _, call := range []func() error{
		func() error { return c.Get(ctx, "/v1/things/"+id, nil, nil) },
		func() error { return c.Post(ctx, "/v1/things/"+id, map[string]any{}, nil) },
		func() error { return c.Delete(ctx, "/v1/things/"+id, nil, nil) },
		func() error { return c.Post(ctx, "/v1/things/"+id+"/archive", nil, nil) },
	} {
		var apiErr *anthropic.Error
		if err := call(); !errors.As(err, &apiErr) || apiErr.StatusCode != http.StatusNotFound || apiErr.Type() != "not_found_error" {
			t.Errorf("after delete: %v, want a 404 not_found_error", err)
		}
	}
	if things.Len() != 0 || things.Has(id) {
		t.Error("the collection still holds the deleted thing")
	}
}

func TestCollectionUpdateModes(t *testing.T) {
	for _, tc := range []struct {
		mode UpdateMode
		want string
	}{
		{Replace, `{"id":"x","name":"b","metadata":{"k1":null,"k3":""},"updated_at":"2026-01-01T00:00:01Z"}`},
		{Patch, `{"id":"x","name":"b","metadata":{"k2":"v2","k3":""},"updated_at":"2026-01-01T00:00:01Z"}`},
		{PatchNullOrEmpty, `{"id":"x","name":"b","metadata":{"k2":"v2"},"updated_at":"2026-01-01T00:00:01Z"}`},
	} {
		s := NewServer(t)
		c := s.NewCollection("x_")
		c.Put(map[string]any{"id": "x", "name": "a", "metadata": map[string]any{"k1": "v1", "k2": "v2"}})
		got, ok := c.Update("x", map[string]any{"name": "b", "metadata": map[string]any{"k1": nil, "k3": ""}}, tc.mode)
		if !ok {
			t.Fatal("update of a stored object failed")
		}
		if diff := JSONDiff(render(got), tc.want); diff != "" {
			t.Errorf("mode %d:\n%s", tc.mode, diff)
		}
		if _, ok := c.Update("missing", nil, tc.mode); ok {
			t.Error("update of a missing object succeeded")
		}
	}
	// A patch on a field that was not a map starts from an empty one.
	s := NewServer(t)
	c := s.NewCollection("x_")
	c.Put(map[string]any{"id": "x"})
	got, _ := c.Update("x", map[string]any{"metadata": map[string]any{"k": "v", "gone": nil}}, Patch)
	if diff := JSONDiff(render(got), `{"id":"x","metadata":{"k":"v"},"updated_at":"2026-01-01T00:00:01Z"}`); diff != "" {
		t.Error(diff)
	}
	if _, ok := c.Archive("missing"); ok {
		t.Error("archive of a missing object succeeded")
	}
}

func TestCollectionChildrenAndListing(t *testing.T) {
	s, things, parts := newThingsAPI(t, Replace)
	c := NewAPIKeyClient(t, s.Server, option.WithMaxRetries(0))
	ctx := context.Background()
	for _, color := range []string{"red", "blue", "red", "red"} {
		things.Create(map[string]any{"color": color})
	}
	if err := c.Post(ctx, "/v1/things/thing_000000000000000000000001/parts", map[string]any{"name": "wheel"}, nil); err != nil {
		t.Fatal(err)
	}
	parts.Create(map[string]any{"name": "door", "thing_id": "thing_000000000000000000000002"})

	// A child is found under its parent only.
	if err := c.Get(ctx, "/v1/things/thing_000000000000000000000001/parts/wheel", nil, nil); err != nil {
		t.Errorf("own part: %v", err)
	}
	if err := c.Get(ctx, "/v1/things/thing_000000000000000000000002/parts/wheel", nil, nil); err == nil {
		t.Error("a part was found under another thing")
	}
	var list struct {
		Data []map[string]any `json:"data"`
	}
	if err := c.Get(ctx, "/v1/things/thing_000000000000000000000002/parts", nil, &list); err != nil || len(list.Data) != 1 || list.Data[0]["name"] != "door" {
		t.Errorf("parts of thing 2: %v %v", list.Data, err)
	}

	// Filtered pages; deleting while walking does not skip anything.
	var red []string
	page := func(query string) (ids []string, next string) {
		var p struct {
			Data     []map[string]any `json:"data"`
			NextPage *string          `json:"next_page"`
		}
		if err := c.Get(ctx, "/v1/things"+query, nil, &p); err != nil {
			t.Fatal(err)
		}
		for _, o := range p.Data {
			ids = append(ids, o["id"].(string))
		}
		if p.NextPage != nil {
			next = *p.NextPage
		}
		return ids, next
	}
	ids, next := page("?color=red")
	red = append(red, ids...)
	for _, id := range ids {
		things.Delete(id)
	}
	ids, last := page("?color=red&page=" + next)
	red = append(red, ids...)
	if strings.Join(red, ",") != "thing_000000000000000000000001,thing_000000000000000000000003,thing_000000000000000000000004" || last != "" {
		t.Errorf("red things %q (next %q)", red, last)
	}
	if got := things.List(nil); len(got) != 2 {
		t.Errorf("List(nil) = %v", got)
	}
}

func TestCollectionHandlerErrors(t *testing.T) {
	rec := newRecorderTB(t)
	s := NewServer(rec)
	c := s.NewCollection("x_")
	c.HandleCreate("POST /v1/x")
	c.HandleUpdate("POST /v1/x/{id}", Replace)
	c.HandleGet("GET /v1/nowildcard")
	c.Put(map[string]any{"id": "x1"})
	client := NewAPIKeyClient(t, s.Server, option.WithMaxRetries(0))
	ctx := context.Background()

	for _, path := range []string{"/v1/x", "/v1/x/x1"} {
		if err := client.Post(ctx, path, []any{"not", "an", "object"}, nil); err == nil {
			t.Errorf("POST %s with an array succeeded", path)
		}
		expectFailure(t, rec, "body is not a JSON object")
	}
	if err := client.Get(ctx, "/v1/nowildcard", nil, nil); err == nil {
		t.Error("GET on a route without wildcard succeeded")
	}
	expectFailure(t, rec, "has no wildcard")
}

func TestDeepCopyPanicsOnNonJSON(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("deepCopy accepted a channel")
		}
	}()
	deepCopy(map[string]any{"c": make(chan int)})
}
