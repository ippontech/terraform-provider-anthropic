// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package organizations

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

// Test helpers private to the invite tests, as the RBAC tests keep theirs
// private to their package. Plan, State, DataSourceConfig and Unknown have
// the names, signatures and override rules of the shared schematest builders
// (#404), so once those land these tests switch by calling schematest.Plan
// and friends and deleting the builders below.

// --- Recording stub ---

// recordedRequest is one request a stub received, with its JSON body decoded.
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
// handler's status and JSON body.
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

// assertBetaQuery fails unless the request carries `beta=true`.
func assertBetaQuery(t *testing.T, req recordedRequest) {
	t.Helper()
	if got := req.Query["beta"]; len(got) != 1 || got[0] != "true" {
		t.Errorf("%s %s: beta = %v, want [true]", req.Method, req.Path, got)
	}
}

// --- Plan, state and config builders ---

// Unknown, as an override value, makes the attribute unknown.
var Unknown = unknownMarker{}

type unknownMarker struct{}

// Plan returns a tfsdk.Plan for r: every top-level attribute null, except
// overrides. An override is a tftypes.Value used as is, nil (null), Unknown,
// a string, or a []string (list or set of strings).
func Plan(tb testing.TB, r resource.Resource, overrides map[string]any) tfsdk.Plan {
	tb.Helper()
	s, obj := resourceSchema(tb, r)
	return tfsdk.Plan{Raw: objectValue(tb, obj, overrides), Schema: s}
}

// State returns a tfsdk.State for r built like Plan.
func State(tb testing.TB, r resource.Resource, overrides map[string]any) tfsdk.State {
	tb.Helper()
	s, obj := resourceSchema(tb, r)
	return tfsdk.State{Raw: objectValue(tb, obj, overrides), Schema: s}
}

// resourceSchema returns r's schema and its tftypes.Object type.
func resourceSchema(tb testing.TB, r resource.Resource) (rschema.Schema, tftypes.Object) {
	tb.Helper()
	var resp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		tb.Fatalf("schema: %v", resp.Diagnostics)
	}
	obj, ok := resp.Schema.Type().TerraformType(context.Background()).(tftypes.Object)
	if !ok {
		tb.Fatal("schema type is not a tftypes.Object")
	}
	return resp.Schema, obj
}

// DataSourceConfig returns a tfsdk.Config for d built like Plan.
func DataSourceConfig(tb testing.TB, d datasource.DataSource, overrides map[string]any) tfsdk.Config {
	tb.Helper()
	var resp datasource.SchemaResponse
	d.Schema(context.Background(), datasource.SchemaRequest{}, &resp)
	if resp.Diagnostics.HasError() {
		tb.Fatalf("schema: %v", resp.Diagnostics)
	}
	obj, ok := resp.Schema.Type().TerraformType(context.Background()).(tftypes.Object)
	if !ok {
		tb.Fatal("schema type is not a tftypes.Object")
	}
	return tfsdk.Config{Raw: objectValue(tb, obj, overrides), Schema: resp.Schema}
}

// readDataSource drives d.Read with a DataSourceConfig built from overrides.
func readDataSource(t *testing.T, d datasource.DataSource, overrides map[string]any) *datasource.ReadResponse {
	t.Helper()
	config := DataSourceConfig(t, d, overrides)
	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: config.Schema}}
	d.Read(context.Background(), datasource.ReadRequest{Config: config}, resp)
	return resp
}

func objectValue(tb testing.TB, obj tftypes.Object, overrides map[string]any) tftypes.Value {
	tb.Helper()
	vals := make(map[string]tftypes.Value, len(obj.AttributeTypes))
	for name, typ := range obj.AttributeTypes {
		vals[name] = tftypes.NewValue(typ, nil)
	}
	for name, v := range overrides {
		typ, ok := obj.AttributeTypes[name]
		if !ok {
			tb.Fatalf("the schema has no attribute %q", name)
		}
		tv, err := toValue(typ, v)
		if err != nil {
			tb.Fatalf("attribute %q: %v", name, err)
		}
		vals[name] = tv
	}
	return tftypes.NewValue(obj, vals)
}

func toValue(typ tftypes.Type, v any) (tftypes.Value, error) {
	switch x := v.(type) {
	case tftypes.Value:
		if !x.Type().Equal(typ) {
			return tftypes.Value{}, fmt.Errorf("value of type %s, want %s", x.Type(), typ)
		}
		return x, nil
	case nil:
		return tftypes.NewValue(typ, nil), nil
	case unknownMarker:
		return tftypes.NewValue(typ, tftypes.UnknownValue), nil
	case []string:
		if !typ.Is(tftypes.List{ElementType: tftypes.String}) && !typ.Is(tftypes.Set{ElementType: tftypes.String}) {
			return tftypes.Value{}, fmt.Errorf("[]string for a %s attribute", typ)
		}
		elems := make([]tftypes.Value, len(x))
		for i, s := range x {
			elems[i] = tftypes.NewValue(tftypes.String, s)
		}
		return tftypes.NewValue(typ, elems), nil
	}
	if err := tftypes.ValidateValue(typ, v); err != nil {
		return tftypes.Value{}, err
	}
	return tftypes.NewValue(typ, v), nil
}
