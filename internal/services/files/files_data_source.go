// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package files

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// filesPageLimit is the maximum page size the Files API accepts.
const filesPageLimit = 1000

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &FilesDataSource{}

func NewFilesDataSource() datasource.DataSource {
	return &FilesDataSource{}
}

// FilesDataSource defines the data source implementation.
type FilesDataSource struct {
	client *anthropic.Client
}

// FilesDataSourceModel describes the data source data model.
type FilesDataSourceModel struct {
	IDs   types.List `tfsdk:"ids"`
	Files types.List `tfsdk:"files"`
}

func (d *FilesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_files"
}

func (d *FilesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists files (Files API, generally available). All pages are fetched automatically. " +
			"File content is not returned, only metadata.",
		Attributes: map[string]schema.Attribute{
			"ids": schema.ListAttribute{
				Optional:    true,
				ElementType: types.StringType,
				MarkdownDescription: "Restrict the result to files whose ID is in this list (1 to 100 unique IDs). " +
					"IDs that do not resolve to a visible file, including deleted files, are silently omitted.",
				Validators: []validator.List{
					listvalidator.SizeBetween(1, 100),
					listvalidator.UniqueValues(),
				},
			},
			"files": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "List of files.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Unique file identifier.",
						},
						"filename": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Original filename of the uploaded file.",
						},
						"mime_type": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "MIME type of the file.",
						},
						"size_bytes": schema.Int64Attribute{
							Computed:            true,
							MarkdownDescription: "Size of the file in bytes.",
						},
						"created_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "RFC 3339 timestamp of when the file was created.",
						},
						"downloadable": schema.BoolAttribute{
							Computed:            true,
							MarkdownDescription: "Whether the file can be downloaded.",
						},
						"expires_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "RFC 3339 timestamp of when the file expires and becomes unavailable for download. Null if the file does not expire.",
						},
						"type": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Object type. Always `file`.",
						},
					},
				},
			},
		},
	}
}

func (d *FilesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *FilesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data FilesDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// `ids` is mutually exclusive with `limit` and `page` and always yields a
	// single page, so the page size is only set for an unfiltered listing.
	params := anthropic.FileListParams{}
	if !data.IDs.IsNull() && !data.IDs.IsUnknown() {
		var ids []string
		resp.Diagnostics.Append(data.IDs.ElementsAs(ctx, &ids, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
		params.IDs = ids
	} else {
		params.Limit = anthropic.Int(filesPageLimit)
	}

	pager := d.client.Files.ListAutoPaging(ctx, params)
	fileObjs := make([]attr.Value, 0)
	for pager.Next() {
		file := pager.Current()
		obj, diags := types.ObjectValueFrom(ctx, fileAttrTypes, mapFileMetadataToModel(&file))
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		fileObjs = append(fileObjs, obj)
	}
	if err := pager.Err(); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list files: %s", err))
		return
	}

	filesList, diags := types.ListValue(types.ObjectType{AttrTypes: fileAttrTypes}, fileObjs)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.Files = filesList

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
