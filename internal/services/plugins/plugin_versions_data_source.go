// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package plugins

import (
	"context"
	"fmt"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
)

var _ datasource.DataSource = &PluginVersionsDataSource{}

func NewPluginVersionsDataSource() datasource.DataSource {
	return &PluginVersionsDataSource{}
}

// PluginVersionsDataSource lists a plugin's version history.
type PluginVersionsDataSource struct {
	client *admin.Client
}

type PluginVersionsDataSourceModel struct {
	PluginID types.String `tfsdk:"plugin_id"`
	Versions types.List   `tfsdk:"versions"`
}

func (d *PluginVersionsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_plugin_versions"
}

func (d *PluginVersionsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the versions of a plugin of a Claude Enterprise organization (Plugins API, beta), " +
			"newest first: the first one is the plugin's `latest_version_id`. All pages are fetched." + pluginsDataSourceAuth,
		Attributes: map[string]schema.Attribute{
			"plugin_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The plugin's ID (prefixed `plugin_`).",
				Validators: []validator.String{stringvalidator.RegexMatches(
					regexp.MustCompile(`^plugin_`), "must be a plugin ID (prefixed plugin_)")},
			},
			"versions": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "The plugin's versions, newest first.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id":               computedString("The version's ID (prefixed `pluginver_`)."),
						"plugin_id":        computedString("The plugin's ID."),
						"display_name":     computedString("The manifest's `displayName`, or null."),
						"description":      computedString("The manifest's `description`, or null."),
						"manifest_version": computedString("The manifest's `version`, normalized for display, or null. Not unique."),
						"release_notes":    computedString("The release notes uploaded with the version, or null."),
						"created_by":       createdByDataSourceAttribute("Who stored the version, null when not recorded (versions synchronized from Git)."),
						"content_scan":     contentScanDataSourceAttribute("The version's content scan, null when it was not scanned."),
						"components":       componentsDataSourceAttribute("The version's components, null when not recorded."),
						"reach":            reachDataSourceAttribute("version"),
						"created_at":       computedString("RFC 3339 timestamp of when the version was stored."),
					},
				},
			},
		},
	}
}

func (d *PluginVersionsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if client := configurePluginsDataSource(req, resp); client != nil {
		d.client = client
	}
}

func (d *PluginVersionsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data PluginVersionsDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var elems []attr.Value
	err := listPluginsPages(ctx, d.client, pluginPath(data.PluginID.ValueString(), "versions"), nil, 1000,
		func(v pluginVersionAPIObject) bool {
			obj, diags := pluginVersionObjectValue(&v)
			resp.Diagnostics.Append(diags...)
			elems = append(elems, obj)
			return !diags.HasError()
		})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list the versions of plugin %s: %s", data.PluginID.ValueString(), err))
		return
	}
	if resp.Diagnostics.HasError() {
		return
	}

	list, diags := listOf(pluginVersionAttrTypes, elems)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.Versions = list
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
