// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package vaults

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"

	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &VaultsDataSource{}

func NewVaultsDataSource() datasource.DataSource {
	return &VaultsDataSource{}
}

// VaultsDataSource defines the data source implementation.
type VaultsDataSource struct {
	client *anthropic.Client
}

// VaultsDataSourceModel describes the data source data model.
type VaultsDataSourceModel struct {
	IncludeArchived types.Bool `tfsdk:"include_archived"`
	Vaults          types.List `tfsdk:"vaults"`
}

// vaultAttrTypes describes each element of the "vaults" list; it matches the
// tfsdk tags of vaultModel.
var vaultAttrTypes = map[string]attr.Type{
	"id":           types.StringType,
	"display_name": types.StringType,
	"metadata":     types.MapType{ElemType: types.StringType},
	"type":         types.StringType,
	"created_at":   types.StringType,
	"updated_at":   types.StringType,
	"archived_at":  types.StringType,
}

func (d *VaultsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vaults"
}

func (d *VaultsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the vaults of the workspace (beta). All pages are fetched automatically.",
		Attributes: map[string]schema.Attribute{
			"include_archived": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "When `true`, archived vaults are included in the results. Defaults to `false`, the API's own default.",
			},
			"vaults": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "List of vaults, newest first. Empty (never null) when there are none.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: vaultAttributes(),
				},
			},
		},
	}
}

func (d *VaultsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	pd, ok := req.ProviderData.(*providerdata.ProviderData)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *providerdata.ProviderData, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}

	if !providerrors.RequireDataSourceAPIClient(pd.Client, &resp.Diagnostics) {
		return
	}

	d.client = pd.Client
}

func (d *VaultsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data VaultsDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// include_archived = false is the API default, so only true is sent.
	params := anthropic.BetaVaultListParams{}
	if !data.IncludeArchived.IsNull() && !data.IncludeArchived.IsUnknown() && data.IncludeArchived.ValueBool() {
		params.IncludeArchived = anthropic.Bool(true)
	}

	pager := d.client.Beta.Vaults.ListAutoPaging(ctx, params)
	vaultObjs := make([]attr.Value, 0)
	for pager.Next() {
		vault := pager.Current()
		common, diags := mapVaultCommon(&vault)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		obj, diags := types.ObjectValueFrom(ctx, vaultAttrTypes, common)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		vaultObjs = append(vaultObjs, obj)
	}
	if err := pager.Err(); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list vaults: %s", err))
		return
	}

	vaultsList, diags := types.ListValue(types.ObjectType{AttrTypes: vaultAttrTypes}, vaultObjs)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.Vaults = vaultsList

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
