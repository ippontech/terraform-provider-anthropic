// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package claudetag

import (
	"context"
	"fmt"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
)

var (
	_ datasource.DataSource = &BundleDataSource{}
	_ datasource.DataSource = &BundlesDataSource{}
)

var bundleAttrTypes = map[string]attr.Type{
	"id":           types.StringType,
	"display_name": types.StringType,
	"description":  types.StringType,
	"created_at":   types.StringType,
	"updated_at":   types.StringType,
}

// bundleAttributes are a Bundle's fields; id is passed in, since the single
// data source takes it as input.
func bundleAttributes(id schema.StringAttribute) map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"id":           id,
		"display_name": schema.StringAttribute{Computed: true, MarkdownDescription: "The Bundle's name."},
		"description":  schema.StringAttribute{Computed: true, MarkdownDescription: "What the Bundle is for; null when it has none."},
		"created_at":   schema.StringAttribute{Computed: true, MarkdownDescription: "RFC 3339 timestamp of when the Bundle was created."},
		"updated_at":   schema.StringAttribute{Computed: true, MarkdownDescription: "RFC 3339 timestamp of the Bundle's last change."},
	}
}

func bundleValues(b *bundleAPIObject) map[string]attr.Value {
	return map[string]attr.Value{
		"id":           types.StringValue(b.ID),
		"display_name": types.StringValue(b.DisplayName),
		"description":  tfvalue.StringOrNull(b.Description),
		"created_at":   tfvalue.StringOrNull(b.CreatedAt),
		"updated_at":   tfvalue.StringOrNull(b.UpdatedAt),
	}
}

// --- anthropic_claude_tag_bundle ---

func NewBundleDataSource() datasource.DataSource {
	return &BundleDataSource{}
}

// BundleDataSource reads one Access bundle by ID.
type BundleDataSource struct {
	client *admin.Client
}

type BundleDataSourceModel struct {
	ID          types.String `tfsdk:"id"`
	DisplayName types.String `tfsdk:"display_name"`
	Description types.String `tfsdk:"description"`
	CreatedAt   types.String `tfsdk:"created_at"`
	UpdatedAt   types.String `tfsdk:"updated_at"`
}

func (d *BundleDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_claude_tag_bundle"
}

func (d *BundleDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads one Claude Tag Access bundle of the organization by ID (Claude Tag API, beta, Claude " +
			"Enterprise): its name and description, never what it grants. Bundles are created and edited in claude.ai." + claudeTagAuth,
		Attributes: bundleAttributes(schema.StringAttribute{
			Required:            true,
			MarkdownDescription: "The Bundle's ID (prefixed `capp_`).",
			Validators:          []validator.String{stringvalidator.RegexMatches(bundleIDPattern, "must be a Bundle ID (prefixed capp_)")},
		}),
	}
}

func (d *BundleDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if client := configureDataSource(req, resp); client != nil {
		d.client = client
	}
}

func (d *BundleDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data BundleDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var bundle bundleAPIObject
	if err := callJSON(ctx, d.client, http.MethodGet, bundlePath(data.ID.ValueString()), nil, nil, &bundle); err != nil {
		if admin.IsNotFound(err) {
			resp.Diagnostics.AddError("Claude Tag Bundle Not Found", fmt.Sprintf("No Bundle %s in the organization (Bundles "+
				"that Claude Tag manages for itself are not readable either). %s", data.ID.ValueString(), err))
			return
		}
		resp.Diagnostics.AddError("Client Error", errorDetail("read Claude Tag bundle "+data.ID.ValueString(), err))
		return
	}

	data.ID = types.StringValue(bundle.ID)
	data.DisplayName = types.StringValue(bundle.DisplayName)
	data.Description = tfvalue.StringOrNull(bundle.Description)
	data.CreatedAt = tfvalue.StringOrNull(bundle.CreatedAt)
	data.UpdatedAt = tfvalue.StringOrNull(bundle.UpdatedAt)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- anthropic_claude_tag_bundles ---

func NewBundlesDataSource() datasource.DataSource {
	return &BundlesDataSource{}
}

// BundlesDataSource lists the organization's Access bundles.
type BundlesDataSource struct {
	client *admin.Client
}

type BundlesDataSourceModel struct {
	Bundles types.List `tfsdk:"bundles"`
}

func (d *BundlesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_claude_tag_bundles"
}

func (d *BundlesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the organization's Claude Tag Access bundles, newest first (Claude Tag API, beta, Claude " +
			"Enterprise). All pages are fetched. Bundles that Claude Tag manages for itself are not listed." + claudeTagAuth,
		Attributes: map[string]schema.Attribute{
			"bundles": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "The Bundles, newest first.",
				NestedObject: schema.NestedAttributeObject{Attributes: bundleAttributes(schema.StringAttribute{
					Computed: true, MarkdownDescription: "The Bundle's ID (prefixed `capp_`).",
				})},
			},
		},
	}
}

func (d *BundlesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if client := configureDataSource(req, resp); client != nil {
		d.client = client
	}
}

func (d *BundlesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	bundles, err := listAll[bundleAPIObject](ctx, d.client, bundlesPath, nil)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", errorDetail("list Claude Tag bundles", err))
		return
	}
	list, diags := bundlesList(bundles)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &BundlesDataSourceModel{Bundles: list})...)
}

func bundlesList(bundles []bundleAPIObject) (types.List, diag.Diagnostics) {
	var diags diag.Diagnostics
	elemType := types.ObjectType{AttrTypes: bundleAttrTypes}
	elems := make([]attr.Value, 0, len(bundles))
	for i := range bundles {
		obj, d := types.ObjectValue(bundleAttrTypes, bundleValues(&bundles[i]))
		diags.Append(d...)
		elems = append(elems, obj)
	}
	if diags.HasError() {
		return types.ListNull(elemType), diags
	}
	list, d := types.ListValue(elemType, elems)
	diags.Append(d...)
	return list, diags
}
