// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package memorystores

import (
	"context"
	"fmt"
	"regexp"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// memoryPathPrefixRegexp requires a path_prefix to be segment-aligned: it
// must start and end with "/" (the API rejects a path_prefix that does not
// end with "/").
var memoryPathPrefixRegexp = regexp.MustCompile(`^/(.*/)?$`)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &MemoriesDataSource{}

func NewMemoriesDataSource() datasource.DataSource {
	return &MemoriesDataSource{}
}

// MemoriesDataSource defines the data source implementation.
type MemoriesDataSource struct {
	client *anthropic.Client
}

// memoriesDSModel describes the "anthropic_memories" data source data model.
type memoriesDSModel struct {
	MemoryStoreID  types.String `tfsdk:"memory_store_id"`
	PathPrefix     types.String `tfsdk:"path_prefix"`
	Depth          types.Int64  `tfsdk:"depth"`
	IncludeContent types.Bool   `tfsdk:"include_content"`
	Memories       types.List   `tfsdk:"memories"`
	Prefixes       types.List   `tfsdk:"prefixes"`
}

// memoriesDSItemAttrTypes describes the attribute types of each element in
// the "memories" list.
var memoriesDSItemAttrTypes = map[string]attr.Type{
	"id":                 types.StringType,
	"path":               types.StringType,
	"content":            types.StringType,
	"content_sha256":     types.StringType,
	"content_size_bytes": types.Int64Type,
	"memory_version_id":  types.StringType,
	"created_at":         types.StringType,
	"updated_at":         types.StringType,
}

func (d *MemoriesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_memories"
}

func (d *MemoriesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists memories in a memory store (beta). All pages are fetched automatically.",
		Attributes: map[string]schema.Attribute{
			// --- Required ---
			"memory_store_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the memory store to list memories from (e.g. `memstore_...`).",
			},

			// --- Optional ---
			"path_prefix": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Optional path prefix filter. Must start with and end with `/` (segment-aligned), e.g. `/notes/`.",
				Validators: []validator.String{
					stringvalidator.RegexMatches(memoryPathPrefixRegexp, "must start with \"/\" and end with \"/\""),
				},
			},
			"depth": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "`0` (or omitted) returns all descendants below `path_prefix` (recursive). `1` returns immediate children only; deeper entries roll up into `prefixes`.",
				Validators: []validator.Int64{
					int64validator.OneOf(0, 1),
				},
			},
			"include_content": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "When `true`, populates `content` on each memory (capping the page size fetched per request at 20). Defaults to `false`, in which case `content` is null on every item.",
			},

			// --- Computed ---
			"memories": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "List of memories.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Unique identifier for this memory (a `mem_...` value).",
						},
						"path": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Hierarchical path of the memory within the store.",
						},
						"content": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "The memory's UTF-8 text content. Null unless `include_content = true`.",
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
						"created_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Creation timestamp (RFC 3339).",
						},
						"updated_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Last update timestamp (RFC 3339).",
						},
					},
				},
			},
			"prefixes": schema.ListAttribute{
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Rolled-up path prefixes (only non-empty when `depth = 1`), each including a trailing `/`.",
			},
		},
	}
}

func (d *MemoriesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *MemoriesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data memoriesDSModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	params := anthropic.BetaMemoryStoreMemoryListParams{
		View: anthropic.BetaManagedAgentsMemoryViewBasic,
	}
	if !data.PathPrefix.IsNull() && !data.PathPrefix.IsUnknown() {
		params.PathPrefix = param.NewOpt(data.PathPrefix.ValueString())
	}
	if !data.Depth.IsNull() && !data.Depth.IsUnknown() {
		params.Depth = param.NewOpt(data.Depth.ValueInt64())
	}
	if !data.IncludeContent.IsNull() && !data.IncludeContent.IsUnknown() && data.IncludeContent.ValueBool() {
		params.View = anthropic.BetaManagedAgentsMemoryViewFull
	}

	pager := d.client.Beta.MemoryStores.Memories.ListAutoPaging(ctx, data.MemoryStoreID.ValueString(), params)
	memoryObjs := make([]attr.Value, 0)
	prefixes := make([]attr.Value, 0)
	for pager.Next() {
		item := pager.Current()
		switch item.Type {
		case "memory_prefix":
			prefix := item.AsMemoryPrefix()
			prefixes = append(prefixes, types.StringValue(prefix.Path))
		default:
			memory := item.AsMemory()
			obj, diags := mapMemoryDSToListObject(&memory)
			resp.Diagnostics.Append(diags...)
			if resp.Diagnostics.HasError() {
				return
			}
			memoryObjs = append(memoryObjs, obj)
		}
	}
	if err := pager.Err(); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list memories: %s", err))
		return
	}

	memoriesList, diags := types.ListValue(types.ObjectType{AttrTypes: memoriesDSItemAttrTypes}, memoryObjs)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.Memories = memoriesList

	prefixesList, diags := types.ListValue(types.StringType, prefixes)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.Prefixes = prefixesList

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// mapMemoryDSToListObject maps a single memory into the "memories" list's
// nested object value, via the mapping shared with the resource and the
// singular data source (see memory_common.go).
func mapMemoryDSToListObject(memory *anthropic.BetaManagedAgentsMemory) (attr.Value, diag.Diagnostics) {
	common := mapMemoryCommon(memory)

	return types.ObjectValue(memoriesDSItemAttrTypes, map[string]attr.Value{
		"id":                 common.ID,
		"path":               common.Path,
		"content":            common.Content,
		"content_sha256":     common.ContentSha256,
		"content_size_bytes": common.ContentSizeBytes,
		"memory_version_id":  common.MemoryVersionID,
		"created_at":         common.CreatedAt,
		"updated_at":         common.UpdatedAt,
	})
}
