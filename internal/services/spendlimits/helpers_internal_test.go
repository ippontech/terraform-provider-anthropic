// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package spendlimits

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
)

// recordedRequest is one request an httptest stub received.
type recordedRequest struct {
	Method string
	Path   string
	Query  map[string][]string
	Beta   string
	Body   map[string]any
}

type recorder struct {
	mu       sync.Mutex
	requests []recordedRequest
}

func (rec *recorder) record(r *http.Request) recordedRequest {
	req := recordedRequest{Method: r.Method, Path: r.URL.Path, Query: r.URL.Query(), Beta: r.Header.Get("anthropic-beta")}
	if b, _ := io.ReadAll(r.Body); len(b) > 0 {
		_ = json.Unmarshal(b, &req.Body)
	}
	rec.mu.Lock()
	rec.requests = append(rec.requests, req)
	rec.mu.Unlock()
	return req
}

func (rec *recorder) all() []recordedRequest {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	return append([]recordedRequest(nil), rec.requests...)
}

// stub starts an httptest server that records each request and answers with
// handler's status and body.
func stub(t *testing.T, handler func(req recordedRequest) (int, string)) (*httptest.Server, *recorder) {
	t.Helper()
	rec := &recorder{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		status, body := handler(rec.record(r))
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

const notFoundBody = `{"type":"error","error":{"type":"not_found_error","message":"not found"}}`

func assertBetaQuery(t *testing.T, req recordedRequest) {
	t.Helper()
	if got := req.Query["beta"]; len(got) != 1 || got[0] != "true" {
		t.Errorf("%s %s: beta = %v, want [true]", req.Method, req.Path, got)
	}
}

// resourceValue builds a resource object value with every attribute null
// except the ones in set.
func resourceValue(t *testing.T, r resource.Resource, set map[string]tftypes.Value) tftypes.Value {
	t.Helper()
	vals := schematest.NullValues(t, r)
	for name, v := range set {
		vals[name] = v
	}
	return tftypes.NewValue(schematest.ResourceObjectType(t, r), vals)
}

// scopeValue builds the resource's scope object; empty strings stay null and
// tftypes.UnknownValue makes an attribute unknown.
func scopeValue(t *testing.T, r resource.Resource, scopeType, userID, workspaceID any) tftypes.Value {
	t.Helper()
	objType := schematest.ResourceObjectType(t, r).AttributeTypes["scope"].(tftypes.Object)
	nullIfEmpty := func(v any) tftypes.Value {
		if v == "" {
			return str(nil)
		}
		return str(v)
	}
	return tftypes.NewValue(objType, map[string]tftypes.Value{
		"type":         nullIfEmpty(scopeType),
		"user_id":      nullIfEmpty(userID),
		"workspace_id": nullIfEmpty(workspaceID),
	})
}

// str builds a string value: nil is null, tftypes.UnknownValue is unknown.
func str(v any) tftypes.Value { return tftypes.NewValue(tftypes.String, v) }
