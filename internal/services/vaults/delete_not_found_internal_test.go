// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package vaults

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"

	"github.com/ippontech/terraform-provider-anthropic/internal/oauthtest"
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
			name: "vault",
			resource: func(srv *httptest.Server) resource.Resource {
				return &VaultResource{client: oauthtest.NewSDKClient(t, srv, option.WithMaxRetries(0))}
			},
			attrs: map[string]any{"id": "vlt_01", "archive_on_destroy": false},
		},
		{
			name: "vault archive_on_destroy",
			resource: func(srv *httptest.Server) resource.Resource {
				return &VaultResource{client: oauthtest.NewSDKClient(t, srv, option.WithMaxRetries(0))}
			},
			attrs: map[string]any{"id": "vlt_01", "archive_on_destroy": true},
		},
		{
			name: "vault_credential",
			resource: func(srv *httptest.Server) resource.Resource {
				return &VaultCredentialResource{client: oauthtest.NewSDKClient(t, srv, option.WithMaxRetries(0))}
			},
			attrs: map[string]any{"id": "vcrd_01", "vault_id": "vlt_01", "archive_on_destroy": false},
		},
		{
			name: "vault_credential archive_on_destroy",
			resource: func(srv *httptest.Server) resource.Resource {
				return &VaultCredentialResource{client: oauthtest.NewSDKClient(t, srv, option.WithMaxRetries(0))}
			},
			attrs: map[string]any{"id": "vcrd_01", "vault_id": "vlt_01", "archive_on_destroy": true},
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
