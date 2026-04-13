// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework-validators/float64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/float64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &MessageResource{}
var _ resource.ResourceWithImportState = &MessageResource{}

func NewMessageResource() resource.Resource {
	return &MessageResource{}
}

// MessageResource defines the resource implementation.
type MessageResource struct {
	client *anthropic.Client
}

// MessageResourceModel describes the resource data model.
type MessageResourceModel struct {
	Model        types.String  `tfsdk:"model"`
	MaxTokens    types.Int64   `tfsdk:"max_tokens"`
	Messages     types.List    `tfsdk:"messages"`
	System       types.String  `tfsdk:"system"`
	Temperature  types.Float64 `tfsdk:"temperature"`
	ID           types.String  `tfsdk:"id"`
	StopReason   types.String  `tfsdk:"stop_reason"`
	Content      types.String  `tfsdk:"content"`
	InputTokens  types.Int64   `tfsdk:"input_tokens"`
	OutputTokens types.Int64   `tfsdk:"output_tokens"`
}

// MessageParamModel describes a single input message.
type MessageParamModel struct {
	Role    types.String `tfsdk:"role"`
	Content types.String `tfsdk:"content"`
}

func (r *MessageResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_message"
}

func (r *MessageResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Sends a message to an Anthropic model via the Messages API and stores the response. All input attributes trigger resource replacement on change.",
		Attributes: map[string]schema.Attribute{
			"model": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The model ID to use (e.g. `claude-haiku-4-5-20251001`).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"max_tokens": schema.Int64Attribute{
				Required:            true,
				MarkdownDescription: "The maximum number of tokens to generate before stopping.",
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.RequiresReplace()},
				Validators:          []validator.Int64{int64validator.AtLeast(1)},
			},
			"messages": schema.ListNestedAttribute{
				Required:            true,
				MarkdownDescription: "Input messages in alternating `user` / `assistant` conversational turns.",
				PlanModifiers:       []planmodifier.List{listplanmodifier.RequiresReplace()},
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
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"temperature": schema.Float64Attribute{
				Optional:            true,
				MarkdownDescription: "Amount of randomness injected into the response (0.0–1.0). Lower values (closer to 0) produce more focused and deterministic responses; higher values produce more creative responses. If not specified, the model uses its default.",
				PlanModifiers:       []planmodifier.Float64{float64planmodifier.RequiresReplace()},
				Validators:          []validator.Float64{float64validator.Between(0.0, 1.0)},
			},
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Unique message identifier assigned by the API.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"stop_reason": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The reason the model stopped generating tokens (e.g., `end_turn` if the model finished naturally, or `max_tokens` if the max_tokens limit was reached).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"content": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The generated text response from the model. Only `text` content blocks are included; other block types (e.g., `thinking`) are omitted.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"input_tokens": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Number of input tokens used in the request.",
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"output_tokens": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Number of output tokens generated in the response.",
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *MessageResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*anthropic.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *anthropic.Client, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}

	r.client = client
}

func (r *MessageResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data MessageResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
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

	params := anthropic.MessageNewParams{
		Model:     data.Model.ValueString(),
		MaxTokens: data.MaxTokens.ValueInt64(),
		Messages:  sdkMessages,
	}

	if !data.System.IsNull() && !data.System.IsUnknown() {
		params.System = []anthropic.TextBlockParam{{Text: data.System.ValueString()}}
	}

	if !data.Temperature.IsNull() && !data.Temperature.IsUnknown() {
		params.Temperature = anthropic.Float(data.Temperature.ValueFloat64())
	}

	message, err := r.client.Messages.New(ctx, params)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create message: %s", err))
		return
	}

	data.ID = types.StringValue(message.ID)
	data.StopReason = types.StringValue(string(message.StopReason))
	data.InputTokens = types.Int64Value(message.Usage.InputTokens)
	data.OutputTokens = types.Int64Value(message.Usage.OutputTokens)

	var parts []string
	for _, block := range message.Content {
		if block.Type == "text" {
			parts = append(parts, block.Text)
		}
	}
	data.Content = types.StringValue(strings.Join(parts, ""))

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *MessageResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	// The Messages API has no GET endpoint; preserve existing state as-is.
	var data MessageResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *MessageResource) Update(_ context.Context, _ resource.UpdateRequest, _ *resource.UpdateResponse) {
	// Never called: all input attributes have RequiresReplace plan modifiers.
}

func (r *MessageResource) Delete(_ context.Context, _ resource.DeleteRequest, _ *resource.DeleteResponse) {
	// The Messages API has no DELETE endpoint; removing from state is sufficient.
}

func (r *MessageResource) ImportState(_ context.Context, _ resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resp.Diagnostics.AddError(
		"Import Not Supported",
		"anthropic_message does not support import. The Messages API has no GET endpoint to retrieve a previously created message.",
	)
}
