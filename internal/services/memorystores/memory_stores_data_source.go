// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package memorystores

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &MemoryStoresDataSource{}

func NewMemoryStoresDataSource() datasource.DataSource {
	return &MemoryStoresDataSource{}
}

// MemoryStoresDataSource defines the data source implementation.
type MemoryStoresDataSource struct {
	client *anthropic.Client
}

// MemoryStoresDataSourceModel describes the data source data model.
type MemoryStoresDataSourceModel struct {
	IncludeArchived types.Bool `tfsdk:"include_archived"`
	MemoryStores    types.List `tfsdk:"memory_stores"`
}

// memoryStoreListItemAttrTypes describes the attribute types of each element
// in the "memory_stores" list.
var memoryStoreListItemAttrTypes = map[string]attr.Type{
	"id":          types.StringType,
	"name":        types.StringType,
	"description": types.StringType,
	"metadata":    types.MapType{ElemType: types.StringType},
	"created_at":  types.StringType,
	"archived_at": types.StringType,
}

func (d *MemoryStoresDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_memory_stores"
}

func (d *MemoryStoresDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists memory stores (beta). All pages are fetched automatically.",
		Attributes: map[string]schema.Attribute{
			"include_archived": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "When `true`, archived stores are included in the results. Defaults to `false`.",
			},
			"memory_stores": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "List of memory stores.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Unique identifier for the memory store.",
						},
						"name": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Human-readable name for the store.",
						},
						"description": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Free-text description of what the store contains.",
						},
						"metadata": schema.MapAttribute{
							Computed:            true,
							ElementType:         types.StringType,
							MarkdownDescription: "Arbitrary key-value tags attached to the store.",
						},
						"created_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Creation timestamp (RFC 3339).",
						},
						"archived_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Archive timestamp (RFC 3339). Null if the store has not been archived.",
						},
					},
				},
			},
		},
	}
}

func (d *MemoryStoresDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *MemoryStoresDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data MemoryStoresDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	params := anthropic.BetaMemoryStoreListParams{}
	if !data.IncludeArchived.IsNull() && !data.IncludeArchived.IsUnknown() && data.IncludeArchived.ValueBool() {
		params.IncludeArchived = anthropic.Bool(true)
	}

	pager := d.client.Beta.MemoryStores.ListAutoPaging(ctx, params)
	storeObjs := make([]attr.Value, 0)
	for pager.Next() {
		store := pager.Current()
		obj, diags := mapMemoryStoreToListObject(&store)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		storeObjs = append(storeObjs, obj)
	}
	if err := pager.Err(); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list memory stores: %s", err))
		return
	}

	storesList, diags := types.ListValue(types.ObjectType{AttrTypes: memoryStoreListItemAttrTypes}, storeObjs)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.MemoryStores = storesList

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func mapMemoryStoreToListObject(store *anthropic.BetaManagedAgentsMemoryStore) (attr.Value, diag.Diagnostics) {
	common, diags := mapMemoryStoreCommon(store)
	if diags.HasError() {
		return nil, diags
	}

	obj, d := types.ObjectValue(memoryStoreListItemAttrTypes, map[string]attr.Value{
		"id":          common.ID,
		"name":        common.Name,
		"description": common.Description,
		"metadata":    common.Metadata,
		"created_at":  common.CreatedAt,
		"archived_at": common.ArchivedAt,
	})
	diags.Append(d...)
	return obj, diags
}
