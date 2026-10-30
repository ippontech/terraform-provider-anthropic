// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package rbac

import (
	"context"
	"encoding/json"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
)

// recordedRequest is one request an httptest stub received.
type recordedRequest struct {
	Method string
	Path   string
	Query  map[string][]string
	Body   map[string]any
}

// recorder records every request a stub receives, safely across goroutines.
type recorder struct {
	mu       sync.Mutex
	requests []recordedRequest
}

func (rec *recorder) record(r *http.Request) recordedRequest {
	req := recordedRequest{Method: r.Method, Path: r.URL.Path, Query: r.URL.Query()}
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

// assertBetaQuery fails unless the request carries `beta=true`.
func assertBetaQuery(t *testing.T, req recordedRequest) {
	t.Helper()
	if got := req.Query["beta"]; len(got) != 1 || got[0] != "true" {
		t.Errorf("%s %s: beta = %v, want [true]", req.Method, req.Path, got)
	}
}

// resourceValue builds a resource object value with every attribute null
// except the ones in set.
func resourceValue(t *testing.T, r resource.Resource, set map[string]any) tftypes.Value {
	t.Helper()
	objType := schematest.ResourceObjectType(t, r)
	vals := schematest.NullValues(t, r)
	for name, v := range set {
		vals[name] = tftypes.NewValue(objType.AttributeTypes[name], v)
	}
	return tftypes.NewValue(objType, vals)
}

func resourceState(t *testing.T, r resource.Resource, set map[string]any) tfsdk.State {
	t.Helper()
	return tfsdk.State{Raw: resourceValue(t, r, set), Schema: schematest.ResourceSchema(t, r)}
}

func resourcePlan(t *testing.T, r resource.Resource, set map[string]any) tfsdk.Plan {
	t.Helper()
	return tfsdk.Plan{Raw: resourceValue(t, r, set), Schema: schematest.ResourceSchema(t, r)}
}

func emptyResourceState(t *testing.T, r resource.Resource) tfsdk.State {
	t.Helper()
	return tfsdk.State{Schema: schematest.ResourceSchema(t, r)}
}

// runDataSourceRead drives d.Read with a config whose attributes are null
// except the ones in set.
func runDataSourceRead(t *testing.T, d datasource.DataSource, set map[string]tftypes.Value) *datasource.ReadResponse {
	t.Helper()
	var schemaResp datasource.SchemaResponse
	d.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema: %v", schemaResp.Diagnostics)
	}
	objType := schemaResp.Schema.Type().TerraformType(context.Background()).(tftypes.Object)
	vals := map[string]tftypes.Value{}
	for name, typ := range objType.AttributeTypes {
		vals[name] = tftypes.NewValue(typ, nil)
	}
	maps.Copy(vals, set)

	req := datasource.ReadRequest{Config: tfsdk.Config{Raw: tftypes.NewValue(objType, vals), Schema: schemaResp.Schema}}
	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	d.Read(context.Background(), req, resp)
	return resp
}
