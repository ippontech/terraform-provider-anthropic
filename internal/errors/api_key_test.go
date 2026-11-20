// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package errors

import (
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
)

func newAnthropicClient() *anthropic.Client {
	c := anthropic.NewClient(option.WithAPIKey("test"))
	return &c
}

func newAdminClient() *admin.Client {
	return &admin.Client{ApiKey: "test"}
}

// --- RequireResourceAPIClient ---

func TestRequireResourceAPIClient(t *testing.T) {
	tests := []struct {
		name        string
		client      *anthropic.Client
		wantOk      bool
		wantSummary string
		wantDetail  string
	}{
		{
			name:        "nil client returns false and adds error",
			client:      nil,
			wantOk:      false,
			wantSummary: "Missing API Key",
			wantDetail:  "resource",
		},
		{
			name:   "non-nil client returns true with no diagnostics",
			client: newAnthropicClient(),
			wantOk: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var diags diag.Diagnostics
			got := RequireResourceAPIClient(tc.client, &diags)
			assertGuard(t, got, diags, tc.wantOk, tc.wantSummary, tc.wantDetail)
		})
	}
}

// --- RequireDataSourceAPIClient ---

func TestRequireDataSourceAPIClient(t *testing.T) {
	tests := []struct {
		name        string
		client      *anthropic.Client
		wantOk      bool
		wantSummary string
		wantDetail  string
	}{
		{
			name:        "nil client returns false and adds error",
			client:      nil,
			wantOk:      false,
			wantSummary: "Missing API Key",
			wantDetail:  "data source",
		},
		{
			name:   "non-nil client returns true with no diagnostics",
			client: newAnthropicClient(),
			wantOk: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var diags diag.Diagnostics
			got := RequireDataSourceAPIClient(tc.client, &diags)
			assertGuard(t, got, diags, tc.wantOk, tc.wantSummary, tc.wantDetail)
		})
	}
}

// --- RequireAdminResourceClient ---

func TestRequireAdminResourceClient(t *testing.T) {
	tests := []struct {
		name        string
		client      *admin.Client
		wantOk      bool
		wantSummary string
		wantDetail  string
	}{
		{
			name:        "nil client returns false and adds error",
			client:      nil,
			wantOk:      false,
			wantSummary: "Missing Admin API Key",
			wantDetail:  "resource",
		},
		{
			name:   "non-nil client returns true with no diagnostics",
			client: newAdminClient(),
			wantOk: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var diags diag.Diagnostics
			got := RequireAdminResourceClient(tc.client, &diags)
			assertGuard(t, got, diags, tc.wantOk, tc.wantSummary, tc.wantDetail)
		})
	}
}

// --- RequireAdminDataSourceClient ---

func TestRequireAdminDataSourceClient(t *testing.T) {
	tests := []struct {
		name        string
		client      *admin.Client
		wantOk      bool
		wantSummary string
		wantDetail  string
	}{
		{
			name:        "nil client returns false and adds error",
			client:      nil,
			wantOk:      false,
			wantSummary: "Missing Admin API Key",
			wantDetail:  "data source",
		},
		{
			name:   "non-nil client returns true with no diagnostics",
			client: newAdminClient(),
			wantOk: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var diags diag.Diagnostics
			got := RequireAdminDataSourceClient(tc.client, &diags)
			assertGuard(t, got, diags, tc.wantOk, tc.wantSummary, tc.wantDetail)
		})
	}
}

// --- RequireAdminKeyResourceClient / RequireAdminKeyDataSourceClient ---

func TestRequireAdminKeyClient(t *testing.T) {
	guards := map[string]func(*admin.Client, *diag.Diagnostics) bool{
		"resource":    RequireAdminKeyResourceClient,
		"data source": RequireAdminKeyDataSourceClient,
	}
	for kind, guard := range guards {
		t.Run(kind, func(t *testing.T) {
			var diags diag.Diagnostics
			assertGuard(t, guard(nil, &diags), diags, false, "Missing Admin API Key", kind)
			// Its types take no bearer token: the detail must not offer one.
			if strings.Contains(diags[0].Detail(), "auth_token") {
				t.Errorf("detail %q offers a bearer token", diags[0].Detail())
			}

			diags = nil
			assertGuard(t, guard(newAdminClient(), &diags), diags, true, "", "")
		})
	}
}
