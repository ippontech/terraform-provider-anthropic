// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package claudetag

import (
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"

	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// claudeTagAuth ends every schema description: the credential and the beta.
const claudeTagAuth = " Requires a Claude Enterprise scoped Admin API key with the `read:claude_tag` scope, and " +
	"`write:claude_tag` for changes, in `admin_api_key`; a Claude Console Admin API key does not work. The provider " +
	"sends the `anthropic-beta: experimental-claude-tag-2026-10-21` header."

// configureResource returns the Admin client of a resource, or nil with the
// diagnostic when there is none (or no provider data yet).
func configureResource(req resource.ConfigureRequest, resp *resource.ConfigureResponse) *admin.Client {
	if req.ProviderData == nil {
		return nil
	}
	pd, ok := req.ProviderData.(*providerdata.ProviderData)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *providerdata.ProviderData, got: %T. Please report this issue to the provider developers.", req.ProviderData))
		return nil
	}
	if !providerrors.RequireAdminResourceClient(pd.AdminClient, &resp.Diagnostics) {
		return nil
	}
	return pd.AdminClient
}

// configureDataSource is configureResource for a data source.
func configureDataSource(req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) *admin.Client {
	if req.ProviderData == nil {
		return nil
	}
	pd, ok := req.ProviderData.(*providerdata.ProviderData)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *providerdata.ProviderData, got: %T. Please report this issue to the provider developers.", req.ProviderData))
		return nil
	}
	if !providerrors.RequireAdminDataSourceClient(pd.AdminClient, &resp.Diagnostics) {
		return nil
	}
	return pd.AdminClient
}
