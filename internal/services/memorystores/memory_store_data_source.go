// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package memorystores

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &MemoryStoreDataSource{}

func NewMemoryStoreDataSource() datasource.DataSource {
	return &MemoryStoreDataSource{}
}

// MemoryStoreDataSource defines the data source implementation.
type MemoryStoreDataSource struct {
	client *anthropic.Client
}

// MemoryStoreDataSourceModel describes the data source data model. It has no
// archive_on_destroy attribute, unlike MemoryStoreResourceModel, so it cannot
// simply reuse that type.
type MemoryStoreDataSourceModel struct {
	ID          types.String `tfsdk:"id"`
	Name        types.String `tfsdk:"name"`
	Description types.String `tfsdk:"description"`
	Metadata    types.Map    `tfsdk:"metadata"`
	CreatedAt   types.String `tfsdk:"created_at"`
	ArchivedAt  types.String `tfsdk:"archived_at"`
}

func (d *MemoryStoreDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_memory_store"
}

func (d *MemoryStoreDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Retrieves a memory store by ID (beta). Works regardless of whether the store is archived.",
		Attributes: map[string]schema.Attribute{
			// --- Required ---
			"id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the memory store to retrieve (e.g. `memstore_...`).",
			},

			// --- Computed ---
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
	}
}

func (d *MemoryStoreDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *MemoryStoreDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data MemoryStoreDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	store, err := d.client.Beta.MemoryStores.Get(ctx, data.ID.ValueString(), anthropic.BetaMemoryStoreGetParams{})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to retrieve memory store: %s", err))
		return
	}

	common, diags := mapMemoryStoreCommon(store)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.ID = common.ID
	data.Name = common.Name
	data.Description = common.Description
	data.Metadata = common.Metadata
	data.CreatedAt = common.CreatedAt
	data.ArchivedAt = common.ArchivedAt

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
