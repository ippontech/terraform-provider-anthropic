// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &CountTokensDataSource{}
var _ datasource.DataSourceWithConfigure = &CountTokensDataSource{}

func NewCountTokensDataSource() datasource.DataSource {
	return &CountTokensDataSource{}
}

// CountTokensDataSource defines the data source implementation.
type CountTokensDataSource struct {
	client *anthropic.Client
}

// CountTokensDataSourceModel describes the data source data model.
type CountTokensDataSourceModel struct {
	Model       types.String `tfsdk:"model"`
	Messages    types.List   `tfsdk:"messages"`
	System      types.String `tfsdk:"system"`
	InputTokens types.Int64  `tfsdk:"input_tokens"`
}

func (d *CountTokensDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_count_tokens"
}

func (d *CountTokensDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Counts the number of tokens in a message request without creating it. Useful for estimating costs and validating that requests fit within model context windows.",
		Attributes: map[string]schema.Attribute{
			"model": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The model ID to use for token counting (e.g. `claude-haiku-4-5-20251001`).",
			},
			"messages": schema.ListNestedAttribute{
				Required:            true,
				MarkdownDescription: "Input messages in alternating `user` / `assistant` conversational turns.",
				Validators:          []validator.List{listvalidator.SizeAtLeast(1)},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"role": schema.StringAttribute{
							Required:            true,
							MarkdownDescription: "Conversational role: `user` or `assistant`.",
							Validators:          []validator.String{stringvalidator.OneOf("user", "assistant")},
						},
						"content": schema.StringAttribute{
							Required:            true,
							MarkdownDescription: "Text content of the message.",
						},
					},
				},
			},
			"system": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "System prompt providing context and instructions to the model.",
			},
			"input_tokens": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "The total number of tokens across the provided list of messages, system prompt, and tools.",
			},
		},
	}
}

func (d *CountTokensDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*anthropic.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *anthropic.Client, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}

	d.client = client
}

func (d *CountTokensDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data CountTokensDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var msgModels []MessageParamModel
	resp.Diagnostics.Append(data.Messages.ElementsAs(ctx, &msgModels, false)...)
	if resp.Diagnostics.HasError() {
		return
	}

	sdkMessages := make([]anthropic.MessageParam, len(msgModels))
	for i, m := range msgModels {
		block := anthropic.NewTextBlock(m.Content.ValueString())
		switch m.Role.ValueString() {
		case "user":
			sdkMessages[i] = anthropic.NewUserMessage(block)
		case "assistant":
			sdkMessages[i] = anthropic.NewAssistantMessage(block)
		default:
			resp.Diagnostics.AddError(
				"Invalid Message Role",
				fmt.Sprintf("Message at index %d has invalid role %q; must be \"user\" or \"assistant\".", i, m.Role.ValueString()),
			)
			return
		}
	}

	params := anthropic.MessageCountTokensParams{
		Model:    data.Model.ValueString(),
		Messages: sdkMessages,
	}

	if !data.System.IsNull() && !data.System.IsUnknown() {
		params.System = anthropic.MessageCountTokensParamsSystemUnion{
			OfString: anthropic.String(data.System.ValueString()),
		}
	}

	result, err := d.client.Messages.CountTokens(ctx, params)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to count tokens: %s", err))
		return
	}

	data.InputTokens = types.Int64Value(result.InputTokens)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
