// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
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
	Models []ModelModel `tfsdk:"models"`
}

type ModelModel struct {
	ID             types.String `tfsdk:"id"`
	DisplayName    types.String `tfsdk:"display_name"`
	CreatedAt      types.String `tfsdk:"created_at"`
	MaxInputTokens types.Int64  `tfsdk:"max_input_tokens"`
	MaxTokens      types.Int64  `tfsdk:"max_tokens"`
	Capabilities   types.Object `tfsdk:"capabilities"`
}

var capabilitiesAttrTypes = map[string]attr.Type{
	"batch":              types.BoolType,
	"citations":          types.BoolType,
	"code_execution":     types.BoolType,
	"context_management": types.ObjectType{AttrTypes: contextManagementAttrTypes},
	"effort":             types.ObjectType{AttrTypes: effortAttrTypes},
	"image_input":        types.BoolType,
	"pdf_input":          types.BoolType,
	"structured_outputs": types.BoolType,
	"thinking":           types.BoolType,
}

var contextManagementAttrTypes = map[string]attr.Type{
	"clear_thinking_20251015":  types.BoolType,
	"clear_tool_uses_20250919": types.BoolType,
	"compact_20260112":         types.BoolType,
}

var effortAttrTypes = map[string]attr.Type{
	"high":   types.BoolType,
	"low":    types.BoolType,
	"max":    types.BoolType,
	"medium": types.BoolType,
}

func (d *ModelsDataSource) Metadata(ctx context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_models"
}

func (d *ModelsDataSource) Schema(ctx context.Context, req datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Retrieves the list of available Anthropic models.",
		Attributes: map[string]schema.Attribute{
			"models": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "List of available models, ordered from most recently released to oldest.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Unique model identifier.",
						},
						"display_name": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "A human-readable name for the model.",
						},
						"created_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "RFC 3339 datetime string representing when the model was released.",
						},
						"max_input_tokens": schema.Int64Attribute{
							Computed:            true,
							MarkdownDescription: "Maximum input context window size in tokens.",
						},
						"max_tokens": schema.Int64Attribute{
							Computed:            true,
							MarkdownDescription: "Maximum value for the max_tokens parameter when using this model.",
						},
						"capabilities": schema.SingleNestedAttribute{
							Computed:            true,
							MarkdownDescription: "Model capability flags.",
							Attributes: map[string]schema.Attribute{
								"batch": schema.BoolAttribute{
									Computed:            true,
									MarkdownDescription: "Whether the model supports the Batch API.",
								},
								"citations": schema.BoolAttribute{
									Computed:            true,
									MarkdownDescription: "Whether the model supports citation generation.",
								},
								"code_execution": schema.BoolAttribute{
									Computed:            true,
									MarkdownDescription: "Whether the model supports code execution tools.",
								},
								"context_management": schema.SingleNestedAttribute{
									Computed:            true,
									MarkdownDescription: "Context management strategy support.",
									Attributes: map[string]schema.Attribute{
										"clear_thinking_20251015": schema.BoolAttribute{
											Computed:            true,
											MarkdownDescription: "Whether the model supports the `clear_thinking_20251015` context management strategy.",
										},
										"clear_tool_uses_20250919": schema.BoolAttribute{
											Computed:            true,
											MarkdownDescription: "Whether the model supports the `clear_tool_uses_20250919` context management strategy.",
										},
										"compact_20260112": schema.BoolAttribute{
											Computed:            true,
											MarkdownDescription: "Whether the model supports the `compact_20260112` context management strategy.",
										},
									},
								},
								"effort": schema.SingleNestedAttribute{
									Computed:            true,
									MarkdownDescription: "Reasoning effort (reasoning_effort) level support.",
									Attributes: map[string]schema.Attribute{
										"high": schema.BoolAttribute{
											Computed:            true,
											MarkdownDescription: "Whether the model supports `high` effort level.",
										},
										"low": schema.BoolAttribute{
											Computed:            true,
											MarkdownDescription: "Whether the model supports `low` effort level.",
										},
										"max": schema.BoolAttribute{
											Computed:            true,
											MarkdownDescription: "Whether the model supports `max` effort level.",
										},
										"medium": schema.BoolAttribute{
											Computed:            true,
											MarkdownDescription: "Whether the model supports `medium` effort level.",
										},
									},
								},
								"image_input": schema.BoolAttribute{
									Computed:            true,
									MarkdownDescription: "Whether the model accepts image content blocks.",
								},
								"pdf_input": schema.BoolAttribute{
									Computed:            true,
									MarkdownDescription: "Whether the model accepts PDF content blocks.",
								},
								"structured_outputs": schema.BoolAttribute{
									Computed:            true,
									MarkdownDescription: "Whether the model supports structured output / JSON mode.",
								},
								"thinking": schema.BoolAttribute{
									Computed:            true,
									MarkdownDescription: "Whether the model supports extended thinking.",
								},
							},
						},
					},
				},
			},
		},
	}
}

func (d *ModelsDataSource) Configure(ctx context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *ModelsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data ModelsDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	pager := d.client.Models.ListAutoPaging(ctx, anthropic.ModelListParams{
		Limit: anthropic.Int(1000),
	})

	models := make([]ModelModel, 0)
	for pager.Next() {
		m := pager.Current()

		ctxMgmt, diags := types.ObjectValue(contextManagementAttrTypes, map[string]attr.Value{
			"clear_thinking_20251015":  types.BoolValue(m.Capabilities.ContextManagement.ClearThinking20251015.Supported),
			"clear_tool_uses_20250919": types.BoolValue(m.Capabilities.ContextManagement.ClearToolUses20250919.Supported),
			"compact_20260112":         types.BoolValue(m.Capabilities.ContextManagement.Compact20260112.Supported),
		})
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}

		effort, diags := types.ObjectValue(effortAttrTypes, map[string]attr.Value{
			"high":   types.BoolValue(m.Capabilities.Effort.High.Supported),
			"low":    types.BoolValue(m.Capabilities.Effort.Low.Supported),
			"max":    types.BoolValue(m.Capabilities.Effort.Max.Supported),
			"medium": types.BoolValue(m.Capabilities.Effort.Medium.Supported),
		})
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}

		caps, diags := types.ObjectValue(capabilitiesAttrTypes, map[string]attr.Value{
			"batch":              types.BoolValue(m.Capabilities.Batch.Supported),
			"citations":          types.BoolValue(m.Capabilities.Citations.Supported),
			"code_execution":     types.BoolValue(m.Capabilities.CodeExecution.Supported),
			"context_management": ctxMgmt,
			"effort":             effort,
			"image_input":        types.BoolValue(m.Capabilities.ImageInput.Supported),
			"pdf_input":          types.BoolValue(m.Capabilities.PDFInput.Supported),
			"structured_outputs": types.BoolValue(m.Capabilities.StructuredOutputs.Supported),
			"thinking":           types.BoolValue(m.Capabilities.Thinking.Supported),
		})
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}

		models = append(models, ModelModel{
			ID:             types.StringValue(m.ID),
			DisplayName:    types.StringValue(m.DisplayName),
			CreatedAt:      types.StringValue(m.CreatedAt.Format(time.RFC3339Nano)),
			MaxInputTokens: types.Int64Value(m.MaxInputTokens),
			MaxTokens:      types.Int64Value(m.MaxTokens),
			Capabilities:   caps,
		})
	}

	if err := pager.Err(); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list models: %s", err))
		return
	}

	data.Models = models
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
