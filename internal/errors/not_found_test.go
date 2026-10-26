// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package errors

import (
	stderrors "errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"

	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
)

// sdkError builds an *anthropic.Error the way the SDK does: Error() reads the
// request and response, so both must be set.
func sdkError(status int) *anthropic.Error {
	return &anthropic.Error{
		StatusCode: status,
		Request:    httptest.NewRequest(http.MethodGet, "https://api.anthropic.com/v1/environments/env_1", nil),
		Response:   &http.Response{StatusCode: status},
	}
}

func TestIsNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "SDK 404", err: sdkError(http.StatusNotFound), want: true},
		{name: "wrapped SDK 404", err: fmt.Errorf("get: %w", sdkError(http.StatusNotFound)), want: true},
		{name: "SDK 403", err: sdkError(http.StatusForbidden), want: false},
		{name: "SDK 410", err: sdkError(http.StatusGone), want: false},
		// Admin API errors have their own type and helper (admin.IsNotFound).
		{name: "admin 404", err: &admin.APIError{StatusCode: http.StatusNotFound}, want: false},
		{name: "plain error", err: stderrors.New("404 not found"), want: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsNotFound(tc.err); got != tc.want {
				t.Errorf("IsNotFound(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
