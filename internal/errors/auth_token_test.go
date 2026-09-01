// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package errors

import (
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

func newOAuthClient() *providerdata.OAuthClient {
	c := anthropic.NewClient(option.WithAuthToken("test"))
	return &providerdata.OAuthClient{Client: &c}
}

func TestRequireOAuthResourceClient(t *testing.T) {
	tests := []struct {
		name        string
		client      *providerdata.OAuthClient
		wantOk      bool
		wantSummary string
		wantDetail  string
	}{
		{
			name:        "nil client returns false and adds error",
			client:      nil,
			wantOk:      false,
			wantSummary: "Missing OAuth Token",
			wantDetail:  "resource",
		},
		{
			name:   "non-nil client returns true with no diagnostics",
			client: newOAuthClient(),
			wantOk: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var diags diag.Diagnostics
			got := RequireOAuthResourceClient(tc.client, &diags)
			assertGuard(t, got, diags, tc.wantOk, tc.wantSummary, tc.wantDetail)
		})
	}
}

func TestRequireOAuthDataSourceClient(t *testing.T) {
	tests := []struct {
		name        string
		client      *providerdata.OAuthClient
		wantOk      bool
		wantSummary string
		wantDetail  string
	}{
		{
			name:        "nil client returns false and adds error",
			client:      nil,
			wantOk:      false,
			wantSummary: "Missing OAuth Token",
			wantDetail:  "data source",
		},
		{
			name:   "non-nil client returns true with no diagnostics",
			client: newOAuthClient(),
			wantOk: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var diags diag.Diagnostics
			got := RequireOAuthDataSourceClient(tc.client, &diags)
			assertGuard(t, got, diags, tc.wantOk, tc.wantSummary, tc.wantDetail)
		})
	}
}

// TestRequireOAuthClientNamesTheRightCredential guards the one thing an
// operator needs from this diagnostic: that an Admin API key will not do.
func TestRequireOAuthClientNamesTheRightCredential(t *testing.T) {
	var diags diag.Diagnostics
	RequireOAuthResourceClient(nil, &diags)

	detail := diags[0].Detail()
	for _, want := range []string{"auth_token", "ANTHROPIC_AUTH_TOKEN", "org:admin", "not accepted"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail %q does not mention %q", detail, want)
		}
	}
}

func assertGuard(t *testing.T, got bool, diags diag.Diagnostics, wantOk bool, wantSummary, wantDetail string) {
	t.Helper()

	if got != wantOk {
		t.Fatalf("got %v, want %v", got, wantOk)
	}
	if wantOk {
		if diags.HasError() {
			t.Fatalf("expected no diagnostics, got: %v", diags)
		}
		return
	}
	if !diags.HasError() {
		t.Fatal("expected error diagnostic, got none")
	}
	if diags[0].Summary() != wantSummary {
		t.Errorf("summary = %q, want %q", diags[0].Summary(), wantSummary)
	}
	if !strings.Contains(diags[0].Detail(), wantDetail) {
		t.Errorf("detail %q does not contain %q", diags[0].Detail(), wantDetail)
	}
}
