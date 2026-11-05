// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package providerdata

import (
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	"github.com/ippontech/terraform-provider-anthropic/internal/platform"
)

// OAuthClient is an SDK client authenticated with an org:admin OAuth bearer
// token (Authorization: Bearer) instead of an API key. It is required by the
// endpoints that reject API keys outright, such as the Workload Identity
// Federation admin endpoints.
//
// It is a distinct named type rather than a bare *anthropic.Client on purpose.
// The SDK carries no notion of which credential a client holds, so two bare
// clients are mutually assignable and mixing them up compiles silently — the
// mistake would only surface as a 401 at apply time. Wrapping keeps the
// compiler in the loop.
type OAuthClient struct {
	*anthropic.Client
}

// ProviderData is passed to every resource and data source Configure call.
type ProviderData struct {
	// Platform is what the clients below reach: the Claude API (first party)
	// or a cloud gateway. The zero value means first party. Resources rarely
	// need it: the provider already refuses, at Configure, a resource the
	// platform does not serve (see internal/provider/registry.go).
	Platform platform.Platform
	// Client is the Anthropic SDK client for standard API endpoints.
	Client *anthropic.Client
	// AdminClient handles /v1/organizations/* endpoints using the Admin API key.
	// Nil when admin_api_key is not configured.
	AdminClient *admin.Client
	// OAuthClient handles endpoints that require an org:admin OAuth bearer
	// token and reject API keys. Nil when auth_token is not configured.
	OAuthClient *OAuthClient
}
