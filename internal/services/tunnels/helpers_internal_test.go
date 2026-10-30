// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package tunnels

import (
	"context"
	"maps"
	"net/http"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
)

// recordedRequest is one request seen by a fake API server.
type recordedRequest struct {
	Method string
	Path   string
	Query  string
	Beta   string
	Auth   string
	Body   string
}

// requestLog collects requests from an httptest handler, safely for the
// SDK's concurrent use.
type requestLog struct {
	mu       sync.Mutex
	requests []recordedRequest
}

func (l *requestLog) add(r *http.Request, body string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.requests = append(l.requests, recordedRequest{
		Method: r.Method,
		Path:   r.URL.Path,
		Query:  r.URL.RawQuery,
		Beta:   r.Header.Get("anthropic-beta"),
		Auth:   r.Header.Get("Authorization"),
		Body:   body,
	})
}

func (l *requestLog) all() []recordedRequest {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]recordedRequest(nil), l.requests...)
}

// count returns how many recorded requests used method on path.
func (l *requestLog) count(method, path string) int {
	n := 0
	for _, r := range l.all() {
		if r.Method == method && r.Path == path {
			n++
		}
	}
	return n
}

// resourceValue builds a full object value for r's schema: every attribute
// null except the given overrides. Every attribute of the tunnel resources is
// a string, so the overrides are too.
func resourceValue(t *testing.T, r resource.Resource, overrides map[string]string) tftypes.Value {
	t.Helper()
	objType := schematest.ResourceObjectType(t, r)
	vals := schematest.NullValues(t, r)
	for k, v := range overrides {
		vals[k] = tftypes.NewValue(objType.AttributeTypes[k], v)
	}
	return tftypes.NewValue(objType, vals)
}

func resourceState(t *testing.T, r resource.Resource, overrides map[string]string) tfsdk.State {
	t.Helper()
	return tfsdk.State{Raw: resourceValue(t, r, overrides), Schema: schematest.ResourceSchema(t, r)}
}

func resourcePlan(t *testing.T, r resource.Resource, overrides map[string]string) tfsdk.Plan {
	t.Helper()
	return tfsdk.Plan{Raw: resourceValue(t, r, overrides), Schema: schematest.ResourceSchema(t, r)}
}

// readDataSource drives ds.Read directly (bypassing Configure) with the given
// config attribute overrides applied on top of an otherwise-null config.
func readDataSource(t *testing.T, ds datasource.DataSource, overrides map[string]tftypes.Value) datasource.ReadResponse {
	t.Helper()
	ctx := context.Background()

	var schemaResp datasource.SchemaResponse
	ds.Schema(ctx, datasource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema: %v", schemaResp.Diagnostics)
	}
	sch := schemaResp.Schema

	objType, ok := sch.Type().TerraformType(ctx).(tftypes.Object)
	if !ok {
		t.Fatal("schema type is not a tftypes.Object")
	}
	vals := make(map[string]tftypes.Value, len(objType.AttributeTypes))
	for name, typ := range objType.AttributeTypes {
		vals[name] = tftypes.NewValue(typ, nil)
	}
	maps.Copy(vals, overrides)

	req := datasource.ReadRequest{Config: tfsdk.Config{Raw: tftypes.NewValue(objType, vals), Schema: sch}}
	resp := datasource.ReadResponse{State: tfsdk.State{Schema: sch}}
	ds.Read(ctx, req, &resp)
	return resp
}

const notFoundBody = `{"type":"error","error":{"type":"not_found_error","message":"not found"}}`
