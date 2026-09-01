// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package errors

import (
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/diag"
)

// assertGuard checks the contract every Require*Client guard shares: it
// returns wantOk, and on false it appends exactly the diagnostic naming the
// missing credential and the kind of caller.
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
