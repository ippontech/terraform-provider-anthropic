// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package files

import (
	"context"
	"errors"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &FileDataSource{}

func NewFileDataSource() datasource.DataSource {
	return &FileDataSource{}
}

// FileDataSource defines the data source implementation.
type FileDataSource struct {
	client *anthropic.Client
}

// fileDataModel holds the metadata attributes shared by the singular data
// source and each element of the plural data source's "files" list.
type fileDataModel struct {
	ID           types.String `tfsdk:"id"`
	Filename     types.String `tfsdk:"filename"`
	MimeType     types.String `tfsdk:"mime_type"`
	SizeBytes    types.Int64  `tfsdk:"size_bytes"`
	CreatedAt    types.String `tfsdk:"created_at"`
	Downloadable types.Bool   `tfsdk:"downloadable"`
	ExpiresAt    types.String `tfsdk:"expires_at"`
	Type         types.String `tfsdk:"type"`
}

// fileAttrTypes describes fileDataModel for the plural data source's list.
var fileAttrTypes = map[string]attr.Type{
	"id":           types.StringType,
	"filename":     types.StringType,
	"mime_type":    types.StringType,
	"size_bytes":   types.Int64Type,
	"created_at":   types.StringType,
	"downloadable": types.BoolType,
	"expires_at":   types.StringType,
	"type":         types.StringType,
}

// mapFileMetadataToModel maps an API file to the data source model. An absent
// expires_at (a file that never expires) maps to null.
func mapFileMetadataToModel(file *anthropic.FileMetadata) fileDataModel {
	return fileDataModel{
		ID:           types.StringValue(file.ID),
		Filename:     types.StringValue(file.Filename),
		MimeType:     types.StringValue(file.MimeType),
		SizeBytes:    types.Int64Value(file.SizeBytes),
		CreatedAt:    tfvalue.TimeOrNull(file.CreatedAt),
		Downloadable: types.BoolValue(file.Downloadable),
		ExpiresAt:    tfvalue.TimeOrNull(file.ExpiresAt),
		Type:         types.StringValue(string(file.Type)),
	}
}

func (d *FileDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_file"
}

func (d *FileDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fetches the metadata of a single file by ID (Files API, generally available). " +
			"The file's content is not returned.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Required:            true,
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
	}
}

func (d *FileDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *FileDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var config fileDataModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := config.ID.ValueString()
	file, err := d.client.Files.GetMetadata(ctx, id)
	if err != nil {
		var apierr *anthropic.Error
		if errors.As(err, &apierr) && apierr.StatusCode == 404 {
			resp.Diagnostics.AddError("File Not Found", fmt.Sprintf("No file with ID %q exists (or it has been deleted or has expired).", id))
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read file: %s", err))
		return
	}

	data := mapFileMetadataToModel(file)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
