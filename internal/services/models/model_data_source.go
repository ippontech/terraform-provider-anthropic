// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package models

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
var _ datasource.DataSource = &ModelDataSource{}

func NewModelDataSource() datasource.DataSource {
	return &ModelDataSource{}
}

// ModelDataSource defines the data source implementation.
type ModelDataSource struct {
	client *anthropic.Client
}

// ModelDataSourceModel describes the data source data model: model_id plus
// the ModelModel attributes.
type ModelDataSourceModel struct {
	ModelID        types.String `tfsdk:"model_id"`
	ID             types.String `tfsdk:"id"`
	DisplayName    types.String `tfsdk:"display_name"`
	CreatedAt      types.String `tfsdk:"created_at"`
	LifecycleStage types.String `tfsdk:"lifecycle_stage"`
	DeprecatedAt   types.String `tfsdk:"deprecated_at"`
	RetiresAt      types.String `tfsdk:"retires_at"`
	Line           types.String `tfsdk:"line"`
	MaxInputTokens types.Int64  `tfsdk:"max_input_tokens"`
	MaxTokens      types.Int64  `tfsdk:"max_tokens"`
	Capabilities   types.Object `tfsdk:"capabilities"`
}

func (d *ModelDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_model"
}

func (d *ModelDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attributes := modelAttributes()
	attributes["model_id"] = schema.StringAttribute{
		Required:            true,
		MarkdownDescription: "The model ID or alias to retrieve.",
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Retrieves a specific Anthropic model by ID or alias.",
		Attributes:          attributes,
	}
}

func (d *ModelDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *ModelDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data ModelDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	m, err := d.client.Models.Get(ctx, data.ModelID.ValueString(), anthropic.ModelGetParams{})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to retrieve model: %s", err))
		return
	}

	model, diags := mapModelInfo(m)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	data.ID = model.ID
	data.DisplayName = model.DisplayName
	data.CreatedAt = model.CreatedAt
	data.LifecycleStage = model.LifecycleStage
	data.DeprecatedAt = model.DeprecatedAt
	data.RetiresAt = model.RetiresAt
	data.Line = model.Line
	data.MaxInputTokens = model.MaxInputTokens
	data.MaxTokens = model.MaxTokens
	data.Capabilities = model.Capabilities

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
