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
			"Configure it via the api_key provider argument or the ANTHROPIC_API_KEY environment variable.",
	)
	return false
}

// RequireAdminResourceClient adds a missing-Admin-API-key error diagnostic and returns false when client is nil.
// The caller must return early when this returns false.
func RequireAdminResourceClient(client *admin.Client, diags *diag.Diagnostics) bool {
	return requireAdminClient(client, diags, "resource")
}

// RequireAdminDataSourceClient adds a missing-Admin-API-key error diagnostic and returns false when client is nil.
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
		"This "+kind+" requires an Admin API key. "+
			"Configure it via the admin_api_key provider argument or the ANTHROPIC_ADMIN_API_KEY environment variable.",
	)
	return false
}
