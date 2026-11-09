// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package models

import (
	"context"
	"fmt"
	"slices"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &ModelsDataSource{}

func NewModelsDataSource() datasource.DataSource {
	return &ModelsDataSource{}
}

// ModelsDataSource defines the data source implementation.
type ModelsDataSource struct {
	client *anthropic.Client
}

// ModelsDataSourceModel describes the data source data model.
type ModelsDataSourceModel struct {
	LifecycleStages types.Set    `tfsdk:"lifecycle_stages"`
	Models          []ModelModel `tfsdk:"models"`
}

func (d *ModelsDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_models"
}

func (d *ModelsDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Retrieves the list of available Anthropic models.",
		Attributes: map[string]schema.Attribute{
			"lifecycle_stages": schema.SetAttribute{
				Optional:    true,
				ElementType: types.StringType,
				MarkdownDescription: "Lifecycle stages to list (the API's `lifecycle` filter): any of `active`, " +
					"`deprecated` and `retired`. When omitted, the API lists `active` and `deprecated` models; `retired` " +
					"models are listed only when `retired` is requested.",
				Validators: []validator.Set{
					setvalidator.SizeAtLeast(1),
					setvalidator.ValueStringsAre(stringvalidator.OneOf(
						string(anthropic.ModelInfoLifecycleActive),
						string(anthropic.ModelInfoLifecycleDeprecated),
						string(anthropic.ModelInfoLifecycleRetired),
					)),
				},
			},
			"models": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "List of available models, ordered from most recently released to oldest.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: modelAttributes(),
				},
			},
		},
	}
}

func (d *ModelsDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *ModelsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data ModelsDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	params, diags := buildModelsListParams(ctx, data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	pager := d.client.Models.ListAutoPaging(ctx, params)

	models := make([]ModelModel, 0)
	for pager.Next() {
		m := pager.Current()
		model, diags := mapModelInfo(&m)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		models = append(models, model)
	}

	if err := pager.Err(); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list models: %s", err))
		return
	}

	data.Models = models
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// buildModelsListParams builds the list request: the largest page size, and
// lifecycle_stages when set, sent as the API's lifecycle[]=... filter (sorted
// so the request does not depend on set order).
func buildModelsListParams(ctx context.Context, data ModelsDataSourceModel) (anthropic.ModelListParams, diag.Diagnostics) {
	var diags diag.Diagnostics
	params := anthropic.ModelListParams{
		Limit: anthropic.Int(1000),
	}
	if !data.LifecycleStages.IsNull() && !data.LifecycleStages.IsUnknown() {
		var stages []string
		diags.Append(data.LifecycleStages.ElementsAs(ctx, &stages, false)...)
		if diags.HasError() {
			return params, diags
		}
		slices.Sort(stages)
		params.Lifecycle = stages
	}
	return params, diags
}
