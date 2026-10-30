// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package platform

import (
	"strings"
	"testing"
)

func TestSupportAllows(t *testing.T) {
	tests := []struct {
		name    string
		support Support
		want    bool
	}{
		{"supported", Supported(), true},
		{"unverified", Unverified("open question"), true},
		{"undeclared", Support{}, true},
		{"unavailable", Unavailable("not served"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.support.Allows(); got != tt.want {
				t.Errorf("Allows() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsGateway(t *testing.T) {
	for p, want := range map[Platform]bool{FirstParty: false, "": false, AWS: true, GoogleCloud: true} {
		if got := p.IsGateway(); got != want {
			t.Errorf("%q.IsGateway() = %v, want %v", p, got, want)
		}
	}
}

// TestUnavailableDiagnostic: the diagnostic names the type and the platform,
// gives the reason and links the platform's documentation.
func TestUnavailableDiagnostic(t *testing.T) {
	for _, p := range []Platform{AWS, GoogleCloud} {
		t.Run(string(p), func(t *testing.T) {
			s := Unavailable("The gateway does not serve invites.")

			summary := UnavailableSummary("anthropic_invite", p)
			if want := "anthropic_invite Not Available on " + p.DisplayName(); summary != want {
				t.Errorf("summary = %q, want %q", summary, want)
			}

			detail := UnavailableDetail("anthropic_invite", p, s)
			for _, want := range []string{"anthropic_invite", p.DisplayName(), s.Reason, p.DocURL()} {
				if !strings.Contains(detail, want) {
					t.Errorf("detail %q does not contain %q", detail, want)
				}
			}
		})
	}
}
