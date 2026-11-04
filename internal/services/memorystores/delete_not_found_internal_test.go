// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package memorystores

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"

	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// newDeleteStatusServer answers every request with status and the API's error
// envelope carrying message, and counts the requests it served.
func newDeleteStatusServer(t *testing.T, status int, message string) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		sdktest.WriteAPIError(w, status, "", message)
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
// destroy wants) and still reports any other error, a 404 "Workspace not
// found" included: a single-workspace key answers it to any
// anthropic-workspace-id header (the provider's workspace_id), and the
// object may still exist.
func TestDelete_NotFoundIsSuccess(t *testing.T) {
	tests := []struct {
		name     string
		resource func(c *anthropic.Client) resource.Resource
		attrs    map[string]any
	}{
		{
			name:     "memory_store",
			resource: func(c *anthropic.Client) resource.Resource { return &MemoryStoreResource{client: c} },
			attrs:    map[string]any{"id": "memstore_01", "archive_on_destroy": false},
		},
		{
			name:     "memory_store archive_on_destroy",
			resource: func(c *anthropic.Client) resource.Resource { return &MemoryStoreResource{client: c} },
			attrs:    map[string]any{"id": "memstore_01", "archive_on_destroy": true},
		},
	}
	workspaceHeader := option.WithHeader("anthropic-workspace-id", "wrkspc_01")

	for _, tc := range tests {
		t.Run(tc.name+"/404", func(t *testing.T) {
			srv, calls := newDeleteStatusServer(t, http.StatusNotFound, "Not Found")
			c := sdktest.NewAPIKeyClient(t, srv, option.WithMaxRetries(0))
			if diags := deleteWithState(t, tc.resource(c), tc.attrs); diags.HasError() {
				t.Fatalf("Delete on 404: %v", diags)
			}
			if got := calls.Load(); got != 1 {
				t.Errorf("requests = %d, want 1", got)
			}
		})
		t.Run(tc.name+"/404 with a workspace header", func(t *testing.T) {
			srv, _ := newDeleteStatusServer(t, http.StatusNotFound, "Not Found")
			c := sdktest.NewAPIKeyClient(t, srv, option.WithMaxRetries(0), workspaceHeader)
			if diags := deleteWithState(t, tc.resource(c), tc.attrs); diags.HasError() {
				t.Fatalf("Delete on 404: %v", diags)
			}
		})
		t.Run(tc.name+"/workspace 404", func(t *testing.T) {
			srv, _ := newDeleteStatusServer(t, http.StatusNotFound, "Workspace not found")
			c := sdktest.NewAPIKeyClient(t, srv, option.WithMaxRetries(0), workspaceHeader)
			if diags := deleteWithState(t, tc.resource(c), tc.attrs); !diags.HasError() {
				t.Fatal("Delete on a workspace 404: want an error diagnostic")
			}
		})
		t.Run(tc.name+"/500", func(t *testing.T) {
			srv, _ := newDeleteStatusServer(t, http.StatusInternalServerError, "Internal Server Error")
			c := sdktest.NewAPIKeyClient(t, srv, option.WithMaxRetries(0))
			if diags := deleteWithState(t, tc.resource(c), tc.attrs); !diags.HasError() {
				t.Fatal("Delete on 500: want an error diagnostic")
			}
		})
	}
}
