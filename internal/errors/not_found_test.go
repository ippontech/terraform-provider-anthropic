// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package errors

import (
	"context"
	stderrors "errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// apiError returns the error an SDK client gets for GET path from a fake
// that answers each path below with a fixed error. withWorkspace makes the
// client send anthropic-workspace-id, as the standard client does when the
// provider's workspace_id is set.
func apiError(t *testing.T, path string, withWorkspace bool) error {
	t.Helper()
	srv := sdktest.NewServer(t)
	notFound := func(message string) map[string]any {
		return map[string]any{"type": "error", "error": map[string]any{"type": "not_found_error", "message": message}}
	}
	srv.Respond("GET /v1/agents/agent_1", http.StatusNotFound, notFound("agent agent_1 not found"))
	// What a single-workspace key gets for any anthropic-workspace-id header.
	srv.Respond("GET /v1/workspace_404", http.StatusNotFound, notFound("Workspace not found"))
	srv.Respond("GET /v1/bare_404", http.StatusNotFound, "<html>404 page not found</html>")
	srv.Respond("GET /v1/json_404", http.StatusNotFound, map[string]any{"detail": "not found"})
	srv.Respond("GET /v1/forbidden", http.StatusForbidden, nil)

	opts := []option.RequestOption{option.WithMaxRetries(0)}
	if withWorkspace {
		opts = append(opts, option.WithHeader("anthropic-workspace-id", "wrkspc_01"))
	}
	err := sdktest.NewAPIKeyClient(t, srv.Server, opts...).Get(context.Background(), path, nil, nil)
	if err == nil {
		t.Fatalf("GET %s succeeded", path)
	}
	return err
}

func TestIsNotFound(t *testing.T) {
	tests := []struct {
		name          string
		path          string
		withWorkspace bool
		want          bool
	}{
		{name: "object 404", path: "/v1/agents/agent_1", want: true},
		// A key that needs a workspace header reaches the workspace; the
		// object is not in it.
		{name: "object 404 with a workspace header", path: "/v1/agents/agent_1", withWorkspace: true, want: true},
		// The key cannot use the workspace the header names: the object may
		// exist.
		{name: "workspace 404 with a workspace header", path: "/v1/workspace_404", withWorkspace: true, want: false},
		// No header, so it names the workspace of the path (an organization
		// endpoint such as /v1/organizations/.../workspaces/{id}).
		{name: "workspace 404 without a workspace header", path: "/v1/workspace_404", want: true},
		{name: "404 without a JSON body", path: "/v1/bare_404", want: false},
		{name: "404 without the not_found_error envelope", path: "/v1/json_404", want: false},
		{name: "403", path: "/v1/forbidden", want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := apiError(t, tc.path, tc.withWorkspace)
			if got := IsNotFound(err); got != tc.want {
				t.Errorf("IsNotFound(%v) = %v, want %v", err, got, tc.want)
			}
			if got := IsNotFound(fmt.Errorf("wrapped: %w", err)); got != tc.want {
				t.Errorf("IsNotFound(wrapped %v) = %v, want %v", err, got, tc.want)
			}
		})
	}

	// Not SDK API errors at all.
	for name, err := range map[string]error{
		"nil": nil,
		// Admin API errors have their own type and helper (admin.IsNotFound).
		"admin 404":   &admin.APIError{StatusCode: http.StatusNotFound, ErrType: "not_found_error"},
		"plain error": stderrors.New("404 not found"),
	} {
		if IsNotFound(err) {
			t.Errorf("IsNotFound(%s) = true, want false", name)
		}
	}
}
