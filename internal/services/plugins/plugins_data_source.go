// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package plugins

import (
	"context"
	"fmt"
	"net/url"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
)

var _ datasource.DataSource = &PluginsDataSource{}

func NewPluginsDataSource() datasource.DataSource {
	return &PluginsDataSource{}
}

// PluginsDataSource lists the organization's plugin inventory.
type PluginsDataSource struct {
	client *admin.Client
}

type PluginsDataSourceModel struct {
	OwnerType     types.String `tfsdk:"owner_type"`
	OwnerUserID   types.String `tfsdk:"owner_user_id"`
	MarketplaceID types.String `tfsdk:"marketplace_id"`
	Plugins       types.List   `tfsdk:"plugins"`
}

func (d *PluginsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_plugins"
}

func (d *PluginsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the plugins of a Claude Enterprise organization (Plugins API, beta), in its " +
			"marketplaces and in members' personal ones, newest first, with optional filters (combined with AND). " +
			"All pages are fetched. Plugins Anthropic publishes and standalone skills are not listed." + pluginsDataSourceAuth,
		Attributes: map[string]schema.Attribute{
			"owner_type": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Only `organization`-owned plugins, or only `user` (member-owned) ones.",
				Validators:          []validator.String{stringvalidator.OneOf("organization", "user")},
			},
			"owner_user_id": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Only the plugins of this member (prefixed `user_`), including after they left the " +
					"organization. A value that matches nothing gives an empty list.",
				Validators: []validator.String{stringvalidator.RegexMatches(
					regexp.MustCompile(`^user_`), "must be a user ID (prefixed user_)")},
			},
			"marketplace_id": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Only the plugins of this marketplace (prefixed `marketplace_`). A value that matches nothing gives an empty list.",
				Validators: []validator.String{stringvalidator.RegexMatches(
					regexp.MustCompile(`^marketplace_`), "must be a marketplace ID (prefixed marketplace_)")},
			},
			"plugins": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "The matching plugins, newest first.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: pluginDataSourceAttributes(computedString("The plugin's ID (prefixed `plugin_`).")),
				},
			},
		},
	}
}

func (d *PluginsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if client := configurePluginsDataSource(req, resp); client != nil {
		d.client = client
	}
}

// buildPluginsQuery maps the configured filters onto the list's query.
func buildPluginsQuery(data PluginsDataSourceModel) url.Values {
	q := url.Values{}
	for name, v := range map[string]types.String{
		"owner_type":     data.OwnerType,
		"owner_user_id":  data.OwnerUserID,
		"marketplace_id": data.MarketplaceID,
	} {
		if s := knownString(v); s != "" {
			q.Set(name, s)
		}
	}
	return q
}

func (d *PluginsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data PluginsDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var elems []attr.Value
	err := listPluginsPages(ctx, d.client, pluginsPath, buildPluginsQuery(data), 100, func(p pluginAPIObject) bool {
		obj, diags := pluginObjectValue(&p)
		resp.Diagnostics.Append(diags...)
		elems = append(elems, obj)
		return !diags.HasError()
	})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list plugins: %s", err))
		return
	}
	if resp.Diagnostics.HasError() {
		return
	}

	list, diags := listOf(pluginAttrTypes, elems)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.Plugins = list
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
