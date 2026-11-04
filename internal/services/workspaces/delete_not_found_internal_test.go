// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"

	"github.com/ippontech/terraform-provider-anthropic/internal/admintest"
	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
)

// newDeleteStatusServer answers every request with status and the API's error
// envelope, and counts the requests it served.
func newDeleteStatusServer(t *testing.T, status int) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"type":"error","error":{"type":"error","message":"` + http.StatusText(status) + `"}}`))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

// deleteWithState calls r.Delete with a state holding attrs (all other
// attributes null) and returns its diagnostics.
func deleteWithState(t *testing.T, r resource.Resource, attrs map[string]any) diag.Diagnostics {
	t.Helper()
	ctx := context.Background()
	state := schematest.NullState(t, r)
	for name, v := range attrs {
		if d := state.SetAttribute(ctx, path.Root(name), v); d.HasError() {
			t.Fatalf("set %s: %v", name, d)
		}
	}
	resp := resource.DeleteResponse{State: state}
	r.Delete(ctx, resource.DeleteRequest{State: state}, &resp)
	return resp.Diagnostics
}

// Delete treats a 404 as success (the object is already gone, which is what
// destroy wants) and still reports any other error.
func TestDelete_NotFoundIsSuccess(t *testing.T) {
	tests := []struct {
		name     string
		resource func(srv *httptest.Server) resource.Resource
		attrs    map[string]any
	}{
		{
			name: "workspace",
			resource: func(srv *httptest.Server) resource.Resource {
				return &WorkspaceResource{adminClient: admintest.NewClient(t, srv)}
			},
			attrs: map[string]any{"id": "wrkspc_01"},
		},
		{
			name: "workspace_member",
			resource: func(srv *httptest.Server) resource.Resource {
				return &WorkspaceMemberResource{adminClient: admintest.NewClient(t, srv)}
			},
			attrs: map[string]any{"workspace_id": "wrkspc_01", "user_id": "user_01"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name+"/404", func(t *testing.T) {
			srv, calls := newDeleteStatusServer(t, http.StatusNotFound)
			if diags := deleteWithState(t, tc.resource(srv), tc.attrs); diags.HasError() {
				t.Fatalf("Delete on 404: %v", diags)
			}
			if got := calls.Load(); got != 1 {
				t.Errorf("requests = %d, want 1", got)
			}
		})
		t.Run(tc.name+"/500", func(t *testing.T) {
			srv, _ := newDeleteStatusServer(t, http.StatusInternalServerError)
			if diags := deleteWithState(t, tc.resource(srv), tc.attrs); !diags.HasError() {
				t.Fatal("Delete on 500: want an error diagnostic")
			}
		})
	}
}
