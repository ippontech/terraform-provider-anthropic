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

var _ datasource.DataSource = &PluginDataSource{}

func NewPluginDataSource() datasource.DataSource {
	return &PluginDataSource{}
}

// PluginDataSource reads one plugin of the organization, organization-owned or
// in a member's personal marketplace.
type PluginDataSource struct {
	client *admin.Client
}

func (d *PluginDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_plugin"
}

func (d *PluginDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fetches one plugin of a Claude Enterprise organization by ID (Plugins API, beta), in one " +
			"of the organization's marketplaces or in a member's personal one. Its display fields describe the " +
			"version members are served." + pluginsDataSourceAuth,
		Attributes: pluginDataSourceAttributes(schema.StringAttribute{
			Required:            true,
			MarkdownDescription: "The plugin's ID (prefixed `plugin_`).",
			Validators: []validator.String{stringvalidator.RegexMatches(
				regexp.MustCompile(`^plugin_`), "must be a plugin ID (prefixed plugin_)")},
		}),
	}
}

func (d *PluginDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if client := configurePluginsDataSource(req, resp); client != nil {
		d.client = client
	}
}

func (d *PluginDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var id types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		return
	}

	plugin, err := getPlugin(ctx, d.client, id.ValueString())
	if err != nil {
		if admin.IsNotFound(err) {
			resp.Diagnostics.AddError("Plugin Not Found", fmt.Sprintf("No plugin %s in the organization (the Plugins API "+
				"also answers 404 when it is not available to the organization): %s", id.ValueString(), err))
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read plugin: %s", err))
		return
	}

	obj, diags := pluginObjectValue(plugin)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, obj)...)
}
