// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package errors

import (
	"github.com/hashicorp/terraform-plugin-framework/diag"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// RequireOAuthResourceClient adds a missing-OAuth-token error diagnostic and returns false when no usable client is configured.
// The caller must return early when this returns false.
func RequireOAuthResourceClient(client *providerdata.OAuthClient, diags *diag.Diagnostics) bool {
	return requireOAuthClient(client, diags, "resource")
}

// RequireOAuthDataSourceClient adds a missing-OAuth-token error diagnostic and returns false when no usable client is configured.
// The caller must return early when this returns false.
func RequireOAuthDataSourceClient(client *providerdata.OAuthClient, diags *diag.Diagnostics) bool {
	return requireOAuthClient(client, diags, "data source")
}

// requireOAuthClient checks the embedded SDK client as well as the wrapper.
// OAuthClient adds a level of indirection the API-key and admin guards do not
// have, so a non-nil wrapper around a nil *anthropic.Client would otherwise
// pass the guard and nil-dereference on the first API call — exactly the
// failure this guard exists to turn into a diagnostic.
func requireOAuthClient(client *providerdata.OAuthClient, diags *diag.Diagnostics, kind string) bool {
	if client != nil && client.Client != nil {
		return true
	}
	diags.AddError(
		"Missing OAuth Token",
		"This "+kind+" requires an org:admin OAuth bearer token. "+
			"Configure it via the auth_token provider argument or the ANTHROPIC_AUTH_TOKEN environment variable. "+
			"An Admin API key (admin_api_key / ANTHROPIC_ADMIN_API_KEY) is not accepted on these endpoints.",
	)
	return false
}
