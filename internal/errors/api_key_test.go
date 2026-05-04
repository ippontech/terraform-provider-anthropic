// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package errors

import (
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/ippontech/terraform-provider-anthropic/internal/provider/admin"
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
			if got != tc.wantOk {
				t.Fatalf("got %v, want %v", got, tc.wantOk)
			}
			if tc.wantOk {
				if diags.HasError() {
					t.Fatalf("expected no diagnostics, got: %v", diags)
				}
				return
			}
			if !diags.HasError() {
				t.Fatal("expected error diagnostic, got none")
			}
			if diags[0].Summary() != tc.wantSummary {
				t.Errorf("summary = %q, want %q", diags[0].Summary(), tc.wantSummary)
			}
			if !strings.Contains(diags[0].Detail(), tc.wantDetail) {
				t.Errorf("detail %q does not contain %q", diags[0].Detail(), tc.wantDetail)
			}
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
			if got != tc.wantOk {
				t.Fatalf("got %v, want %v", got, tc.wantOk)
			}
			if tc.wantOk {
				if diags.HasError() {
					t.Fatalf("expected no diagnostics, got: %v", diags)
				}
				return
			}
			if !diags.HasError() {
				t.Fatal("expected error diagnostic, got none")
			}
			if diags[0].Summary() != tc.wantSummary {
				t.Errorf("summary = %q, want %q", diags[0].Summary(), tc.wantSummary)
			}
			if !strings.Contains(diags[0].Detail(), tc.wantDetail) {
				t.Errorf("detail %q does not contain %q", diags[0].Detail(), tc.wantDetail)
			}
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
			if got != tc.wantOk {
				t.Fatalf("got %v, want %v", got, tc.wantOk)
			}
			if tc.wantOk {
				if diags.HasError() {
					t.Fatalf("expected no diagnostics, got: %v", diags)
				}
				return
			}
			if !diags.HasError() {
				t.Fatal("expected error diagnostic, got none")
			}
			if diags[0].Summary() != tc.wantSummary {
				t.Errorf("summary = %q, want %q", diags[0].Summary(), tc.wantSummary)
			}
			if !strings.Contains(diags[0].Detail(), tc.wantDetail) {
				t.Errorf("detail %q does not contain %q", diags[0].Detail(), tc.wantDetail)
			}
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
			if got != tc.wantOk {
				t.Fatalf("got %v, want %v", got, tc.wantOk)
			}
			if tc.wantOk {
				if diags.HasError() {
					t.Fatalf("expected no diagnostics, got: %v", diags)
				}
				return
			}
			if !diags.HasError() {
				t.Fatal("expected error diagnostic, got none")
			}
			if diags[0].Summary() != tc.wantSummary {
				t.Errorf("summary = %q, want %q", diags[0].Summary(), tc.wantSummary)
			}
			if !strings.Contains(diags[0].Detail(), tc.wantDetail) {
				t.Errorf("detail %q does not contain %q", diags[0].Detail(), tc.wantDetail)
			}
		})
	}
}
