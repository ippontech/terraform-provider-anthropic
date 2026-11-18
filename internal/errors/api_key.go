// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package errors

import (
	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
)

// RequireResourceAPIClient adds a missing-API-key error diagnostic and returns false when client is nil.
// The caller must return early when this returns false.
func RequireResourceAPIClient(client *anthropic.Client, diags *diag.Diagnostics) bool {
	return requireAPIClient(client, diags, "resource")
}

// RequireDataSourceAPIClient adds a missing-API-key error diagnostic and returns false when client is nil.
// The caller must return early when this returns false.
func RequireDataSourceAPIClient(client *anthropic.Client, diags *diag.Diagnostics) bool {
	return requireAPIClient(client, diags, "data source")
}

func requireAPIClient(client *anthropic.Client, diags *diag.Diagnostics, kind string) bool {
	if client != nil {
		return true
	}
	diags.AddError(
		"Missing API Key",
		"This "+kind+" requires an API key. "+
			"Configure it via the api_key provider argument or the ANTHROPIC_API_KEY environment variable, "+
			"or let the provider mint it through Workload Identity Federation with an api_federation block.",
	)
	return false
}

// RequireAdminResourceClient adds a missing-Admin-API-credential error diagnostic and returns false when client is nil.
// The caller must return early when this returns false.
func RequireAdminResourceClient(client *admin.Client, diags *diag.Diagnostics) bool {
	return requireAdminClient(client, diags, "resource")
}

// RequireAdminDataSourceClient adds a missing-Admin-API-credential error diagnostic and returns false when client is nil.
// The caller must return early when this returns false.
func RequireAdminDataSourceClient(client *admin.Client, diags *diag.Diagnostics) bool {
	return requireAdminClient(client, diags, "data source")
}

func requireAdminClient(client *admin.Client, diags *diag.Diagnostics, kind string) bool {
	if client != nil {
		return true
	}
	diags.AddError(
		"Missing Admin API Key",
		"This "+kind+" calls the Admin API. "+
			"Configure an Admin API key via the admin_api_key provider argument or the ANTHROPIC_ADMIN_API_KEY environment variable, "+
			"or, for a Claude Console organization, an org:admin OAuth bearer token via auth_token (ANTHROPIC_AUTH_TOKEN), a federation block or a cli block. "+
			"An admin_api_key whose value is not known until apply gives no credential at plan time, and the bearer token does not stand in for it.",
	)
	return false
}

// RequireAdminKeyResourceClient is RequireAdminResourceClient for a resource
// that takes ProviderData.AdminKeyClient: a bearer token does not serve it.
// The caller must return early when this returns false.
func RequireAdminKeyResourceClient(client *admin.Client, diags *diag.Diagnostics) bool {
	return requireAdminKeyClient(client, diags, "resource")
}

// RequireAdminKeyDataSourceClient is RequireAdminKeyResourceClient for a data source.
// The caller must return early when this returns false.
func RequireAdminKeyDataSourceClient(client *admin.Client, diags *diag.Diagnostics) bool {
	return requireAdminKeyClient(client, diags, "data source")
}

func requireAdminKeyClient(client *admin.Client, diags *diag.Diagnostics, kind string) bool {
	if client != nil {
		return true
	}
	diags.AddError(
		"Missing Admin API Key",
		"This "+kind+" requires an Admin API key, which an OAuth bearer token does not replace. "+
			"Configure it via the admin_api_key provider argument or the ANTHROPIC_ADMIN_API_KEY environment variable. "+
			"An admin_api_key whose value is not known until apply gives no credential at plan time.",
	)
	return false
}
