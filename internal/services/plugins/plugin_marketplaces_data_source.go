// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package plugins

import (
	"context"
	"fmt"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
)

var _ datasource.DataSource = &PluginMarketplacesDataSource{}

func NewPluginMarketplacesDataSource() datasource.DataSource {
	return &PluginMarketplacesDataSource{}
}

// PluginMarketplacesDataSource lists the organization's plugin marketplaces.
type PluginMarketplacesDataSource struct {
	client *admin.Client
}

type PluginMarketplacesDataSourceModel struct {
	OwnerType    types.String `tfsdk:"owner_type"`
	Source       types.String `tfsdk:"source"`
	Marketplaces types.List   `tfsdk:"marketplaces"`
}

func (d *PluginMarketplacesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_plugin_marketplaces"
}

func (d *PluginMarketplacesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the plugin marketplaces of a Claude Enterprise organization (Plugins API, beta): " +
			"its own and its members' personal ones, newest first. All pages are fetched. The library marketplace " +
			"(where uploads go by default) is listed once something was first put in it; Anthropic's own catalog is " +
			"never listed." + pluginsDataSourceAuth,
		Attributes: map[string]schema.Attribute{
			"owner_type": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Only the `organization`'s marketplaces, or only members' personal (`user`) ones.",
				Validators:          []validator.String{stringvalidator.OneOf("organization", "user")},
			},
			"source": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Only the marketplaces with this source: `manual`, `github`, `gitlab` or `public_git`.",
				Validators:          []validator.String{stringvalidator.OneOf("manual", "github", "gitlab", "public_git")},
			},
			"marketplaces": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "The matching marketplaces, newest first.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: pluginMarketplaceDataSourceAttributes(computedString("The marketplace's ID (prefixed `marketplace_`).")),
				},
			},
		},
	}
}

func (d *PluginMarketplacesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if client := configurePluginsDataSource(req, resp); client != nil {
		d.client = client
	}
}

func (d *PluginMarketplacesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data PluginMarketplacesDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	q := url.Values{}
	if s := knownString(data.OwnerType); s != "" {
		q.Set("owner_type", s)
	}
	if s := knownString(data.Source); s != "" {
		q.Set("source", s)
	}

	var elems []attr.Value
	err := listPluginsPages(ctx, d.client, pluginMarketplacesPath, q, 1000, func(m pluginMarketplaceAPIObject) bool {
		obj, diags := pluginMarketplaceObjectValue(&m)
		resp.Diagnostics.Append(diags...)
		elems = append(elems, obj)
		return !diags.HasError()
	})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list plugin marketplaces: %s", err))
		return
	}
	if resp.Diagnostics.HasError() {
		return
	}

	list, diags := listOf(pluginMarketplaceAttrTypes, elems)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.Marketplaces = list
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
