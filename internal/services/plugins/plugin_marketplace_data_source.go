// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package plugins

import (
	"context"
	"fmt"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
)

var _ datasource.DataSource = &PluginMarketplaceDataSource{}

func NewPluginMarketplaceDataSource() datasource.DataSource {
	return &PluginMarketplaceDataSource{}
}

// PluginMarketplaceDataSource reads one plugin marketplace.
type PluginMarketplaceDataSource struct {
	client *admin.Client
}

func (d *PluginMarketplaceDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_plugin_marketplace"
}

func (d *PluginMarketplaceDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fetches one plugin marketplace of a Claude Enterprise organization by ID (Plugins API, " +
			"beta): one of the organization's, or a member's personal one." + pluginsDataSourceAuth,
		Attributes: pluginMarketplaceDataSourceAttributes(schema.StringAttribute{
			Required:            true,
			MarkdownDescription: "The marketplace's ID (prefixed `marketplace_`).",
			Validators: []validator.String{stringvalidator.RegexMatches(
				regexp.MustCompile(`^marketplace_`), "must be a marketplace ID (prefixed marketplace_)")},
		}),
	}
}

func (d *PluginMarketplaceDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if client := configurePluginsDataSource(req, resp); client != nil {
		d.client = client
	}
}

func (d *PluginMarketplaceDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var id types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		return
	}

	marketplace, err := getPluginMarketplace(ctx, d.client, id.ValueString())
	if err != nil {
		if admin.IsNotFound(err) {
			resp.Diagnostics.AddError("Plugin Marketplace Not Found", fmt.Sprintf("No plugin marketplace %s in the "+
				"organization (the Plugins API also answers 404 when it is not available to the organization): %s", id.ValueString(), err))
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read plugin marketplace: %s", err))
		return
	}

	obj, diags := pluginMarketplaceObjectValue(marketplace)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, obj)...)
}
