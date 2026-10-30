// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package providerdata

import (
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"

	"github.com/ippontech/terraform-provider-anthropic/internal/platform"
)

// TestForPlatform: one SDK client serves the standard routes and, wrapped,
// the Admin API; no OAuth client exists on a gateway.
func TestForPlatform(t *testing.T) {
	c := anthropic.NewClient(option.WithoutEnvironmentDefaults())
	for _, p := range []platform.Platform{platform.AWS, platform.GoogleCloud} {
		pd := ForPlatform(p, &c)
		if pd.Platform != p || pd.Client != &c {
			t.Errorf("%s: Platform = %q, Client = %p, want %q, %p", p, pd.Platform, pd.Client, p, &c)
		}
		if pd.AdminClient == nil {
			t.Errorf("%s: AdminClient is nil", p)
		}
		if pd.OAuthClient != nil {
			t.Errorf("%s: OAuthClient = %v, want nil", p, pd.OAuthClient)
		}
	}
}
