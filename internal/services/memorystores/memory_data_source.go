// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package memorystores

import (
	"context"
	"fmt"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &MemoryDataSource{}

func NewMemoryDataSource() datasource.DataSource {
	return &MemoryDataSource{}
}

// MemoryDataSource defines the data source implementation.
type MemoryDataSource struct {
	client *anthropic.Client
}

// memoryDSModel describes the "anthropic_memory" data source data model.
type memoryDSModel struct {
	MemoryStoreID    types.String `tfsdk:"memory_store_id"`
	ID               types.String `tfsdk:"id"`
	Path             types.String `tfsdk:"path"`
	Content          types.String `tfsdk:"content"`
	ContentSha256    types.String `tfsdk:"content_sha256"`
	ContentSizeBytes types.Int64  `tfsdk:"content_size_bytes"`
	MemoryVersionID  types.String `tfsdk:"memory_version_id"`
	Type             types.String `tfsdk:"type"`
	CreatedAt        types.String `tfsdk:"created_at"`
	UpdatedAt        types.String `tfsdk:"updated_at"`
}

func (d *MemoryDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_memory"
}

func (d *MemoryDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Retrieves a memory by ID from a memory store (beta).",
		Attributes: map[string]schema.Attribute{
			// --- Required ---
			"memory_store_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the memory store the memory belongs to (e.g. `memstore_...`).",
			},
			"id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the memory to retrieve (e.g. `mem_...`).",
			},

			// --- Computed ---
			"path": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Hierarchical path of the memory within the store, e.g. `/projects/foo/notes.md`.",
			},
			"content": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The memory's UTF-8 text content.",
			},
			"content_sha256": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Lowercase hex SHA-256 digest of the UTF-8 `content` bytes.",
			},
			"content_size_bytes": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Size of `content` in bytes (the UTF-8 plaintext length).",
			},
			"memory_version_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "ID of the `memory_version` representing this memory's current content (a `memver_...` value).",
			},
			"type": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Object type. Always `memory`.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Creation timestamp (RFC 3339).",
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Last update timestamp (RFC 3339).",
			},
		},
	}
}

func (d *MemoryDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *MemoryDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data memoryDSModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	memory, err := d.client.Beta.MemoryStores.Memories.Get(ctx, data.ID.ValueString(), anthropic.BetaMemoryStoreMemoryGetParams{
		MemoryStoreID: data.MemoryStoreID.ValueString(),
		View:          anthropic.BetaManagedAgentsMemoryViewFull,
	})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to retrieve memory: %s", err))
		return
	}

	mapMemoryDSToState(memory, &data)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// mapMemoryDSToState maps the API response into the memory data source's
// state model.
func mapMemoryDSToState(memory *anthropic.BetaManagedAgentsMemory, data *memoryDSModel) {
	data.ID = types.StringValue(memory.ID)
	data.Path = types.StringValue(memory.Path)
	data.ContentSha256 = types.StringValue(memory.ContentSha256)
	data.ContentSizeBytes = types.Int64Value(memory.ContentSizeBytes)
	data.MemoryVersionID = types.StringValue(memory.MemoryVersionID)
	data.Type = types.StringValue(string(memory.Type))
	data.CreatedAt = types.StringValue(memory.CreatedAt.Format(time.RFC3339))
	data.UpdatedAt = types.StringValue(memory.UpdatedAt.Format(time.RFC3339))

	if memory.JSON.Content.Valid() {
		data.Content = types.StringValue(memory.Content)
	} else {
		data.Content = types.StringNull()
	}
}
