// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package messages

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework-validators/float64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/float64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/listplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &MessageResource{}
var _ resource.ResourceWithImportState = &MessageResource{}
var _ resource.ResourceWithConfigValidators = &MessageResource{}

func NewMessageResource() resource.Resource {
	return &MessageResource{}
}

// MessageResource defines the resource implementation.
type MessageResource struct {
	client *anthropic.Client
}

// MessageResourceModel describes the resource data model.
type MessageResourceModel struct {
	Model                    types.String       `tfsdk:"model"`
	MaxTokens                types.Int64        `tfsdk:"max_tokens"`
	Messages                 types.List         `tfsdk:"messages"`
	System                   types.String       `tfsdk:"system"`
	Temperature              types.Float64      `tfsdk:"temperature"`
	StopSequences            types.List         `tfsdk:"stop_sequences"`
	Metadata                 *MetadataModel     `tfsdk:"metadata"`
	ServiceTier              types.String       `tfsdk:"service_tier"`
	InferenceGeo             types.String       `tfsdk:"inference_geo"`
	Thinking                 *ThinkingModel     `tfsdk:"thinking"`
	OutputConfig             *OutputConfigModel `tfsdk:"output_config"`
	CacheControl             *CacheControlModel `tfsdk:"cache_control"`
	ID                       types.String       `tfsdk:"id"`
	StopReason               types.String       `tfsdk:"stop_reason"`
	StopSequence             types.String       `tfsdk:"stop_sequence"`
	StopDetails              types.Object       `tfsdk:"stop_details"`
	Content                  types.String       `tfsdk:"content"`
	InputTokens              types.Int64        `tfsdk:"input_tokens"`
	OutputTokens             types.Int64        `tfsdk:"output_tokens"`
	ThinkingTokens           types.Int64        `tfsdk:"thinking_tokens"`
	CacheCreationInputTokens types.Int64        `tfsdk:"cache_creation_input_tokens"`
	CacheReadInputTokens     types.Int64        `tfsdk:"cache_read_input_tokens"`
	UsageServiceTier         types.String       `tfsdk:"usage_service_tier"`
	UsageInferenceGeo        types.String       `tfsdk:"usage_inference_geo"`
}

// MessageParamModel describes a single input message without prompt caching.
// It is shared with the count_tokens data source, whose schema has no
// cache_control, so the resource uses its own MessageResourceParamModel.
type MessageParamModel struct {
	Role    types.String `tfsdk:"role"`
	Content types.String `tfsdk:"content"`
}

// MessageResourceParamModel describes a single input message of the resource.
type MessageResourceParamModel struct {
	Role         types.String       `tfsdk:"role"`
	Content      types.String       `tfsdk:"content"`
	CacheControl *CacheControlModel `tfsdk:"cache_control"`
}

// CacheControlModel describes a prompt-caching breakpoint.
type CacheControlModel struct {
	TTL types.String `tfsdk:"ttl"`
}

// MetadataModel describes the request metadata.
type MetadataModel struct {
	UserID types.String `tfsdk:"user_id"`
}

// ThinkingModel describes the extended thinking configuration.
type ThinkingModel struct {
	Type         types.String `tfsdk:"type"`
	BudgetTokens types.Int64  `tfsdk:"budget_tokens"`
	Display      types.String `tfsdk:"display"`
}

// OutputConfigModel describes the output configuration.
type OutputConfigModel struct {
	Effort types.String         `tfsdk:"effort"`
	Format jsontypes.Normalized `tfsdk:"format"`
}

// stopDetailsAttrTypes is the object type of the computed stop_details attribute.
var stopDetailsAttrTypes = map[string]attr.Type{
	"category":    types.StringType,
	"explanation": types.StringType,
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
						"cache_control": cacheControlAttribute("Create a prompt-caching breakpoint at this message's text block."),
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
			"stop_sequences": schema.ListAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Custom text sequences that make the model stop generating. When one is matched, `stop_reason` is `stop_sequence` and `stop_sequence` holds the matched text.",
				PlanModifiers:       []planmodifier.List{listplanmodifier.RequiresReplace()},
			},
			"metadata": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "An object describing metadata about the request.",
				PlanModifiers:       []planmodifier.Object{objectplanmodifier.RequiresReplace()},
				Attributes: map[string]schema.Attribute{
					"user_id": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "An external identifier for the user associated with the request. Use an opaque identifier (uuid or hash); do not include a name, email address or phone number.",
					},
				},
			},
			"service_tier": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Whether to use priority capacity if available (`auto`) or standard capacity only (`standard_only`).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{stringvalidator.OneOf("auto", "standard_only")},
			},
			"inference_geo": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Geographic region for inference processing: `us` or `global`. If omitted, the workspace default applies. Not every model supports it (`claude-haiku-4-5-20251001` answers HTTP 400).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{stringvalidator.OneOf("us", "global")},
			},
			"thinking": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Extended thinking configuration. If omitted, the model's default behaviour applies. Note: `{ type = \"disabled\" }` is rejected by the API (HTTP 400) on claude-fable-5-1, claude-opus-5-5, and claude-sonnet-5-5 (verified 2026-10-08); omit the block instead or use `type = \"adaptive\"` with `output_config.effort` to control thinking behavior.",
				PlanModifiers:       []planmodifier.Object{objectplanmodifier.RequiresReplace()},
				Attributes: map[string]schema.Attribute{
					"type": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "Thinking mode: `enabled` (requires `budget_tokens`), `adaptive` (the model decides) or `disabled`. `adaptive` is not supported by every model (`claude-haiku-4-5-20251001` rejects it). Thinking content is never exposed in `content`; `thinking_tokens` shows its cost.",
						Validators:          []validator.String{stringvalidator.OneOf("enabled", "disabled", "adaptive")},
					},
					"budget_tokens": schema.Int64Attribute{
						Optional:            true,
						MarkdownDescription: "Maximum number of tokens the model may spend on thinking (at least `1024`, and lower than `max_tokens`). Required when `type` is `enabled`, not allowed otherwise.",
						Validators:          []validator.Int64{int64validator.AtLeast(1024)},
					},
					"display": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "How thinking content is returned: `summarized` or `omitted`. Not allowed when `type` is `disabled`.",
						Validators:          []validator.String{stringvalidator.OneOf("summarized", "omitted")},
					},
				},
			},
			"output_config": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Configuration options for the model's output.",
				PlanModifiers:       []planmodifier.Object{objectplanmodifier.RequiresReplace()},
				Attributes: map[string]schema.Attribute{
					"effort": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "How much effort the model puts into the response: `low`, `medium`, `high`, `xhigh` or `max`. Not every model supports every level.",
						Validators:          []validator.String{stringvalidator.OneOf("low", "medium", "high", "xhigh", "max")},
					},
					"format": schema.StringAttribute{
						Optional:            true,
						CustomType:          jsontypes.NormalizedType{},
						MarkdownDescription: "JSON schema object (as a JSON string, e.g. via `jsonencode()`) the response must conform to (structured outputs). The provider wraps it as `{ \"type\": \"json_schema\", \"schema\": ... }`; pass only the schema. `content` then holds the JSON reply, readable with `jsondecode()`.",
					},
				},
			},
			"cache_control": cacheControlAttribute("Top-level prompt caching: automatically places a cache breakpoint on the last cacheable block of the request (the cached prefix includes the system prompt). A breakpoint on the `system` prompt alone is tracked in [#312](https://github.com/ippontech/terraform-provider-anthropic/issues/312). When a top-level and a message-level breakpoint land on the same block their `ttl` must match, and a `1h` breakpoint must come before any `5m` one. Prompts below the model's minimum cacheable length are accepted but not cached; check `cache_creation_input_tokens` and `cache_read_input_tokens`."),
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
			"stop_sequence": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The custom stop sequence that was matched, or null when generation did not stop on one.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"stop_details": schema.SingleNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Structured details about a refusal (`stop_reason` of `refusal`), or null when the response carries none.",
				PlanModifiers:       []planmodifier.Object{objectplanmodifier.UseStateForUnknown()},
				Attributes: map[string]schema.Attribute{
					"category": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "The policy category that triggered the refusal.",
					},
					"explanation": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "Human-readable explanation of the refusal.",
					},
				},
			},
			"thinking_tokens": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Number of output tokens the model spent on internal reasoning (included in `output_tokens`).",
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"cache_creation_input_tokens": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Number of input tokens used to create a prompt cache entry.",
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"cache_read_input_tokens": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Number of input tokens read from the prompt cache.",
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"usage_service_tier": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The service tier the request was actually served with (`standard`, `priority` or `batch`), when reported.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"usage_inference_geo": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The geographic region the request was actually processed in, when reported.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

// cacheControlAttribute builds the shared `{ ttl }` prompt-caching block.
func cacheControlAttribute(description string) schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Optional:            true,
		MarkdownDescription: description + " Set to `{}` for the default 5 minute lifetime.",
		PlanModifiers:       []planmodifier.Object{objectplanmodifier.RequiresReplace()},
		Attributes: map[string]schema.Attribute{
			"ttl": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Cache lifetime: `5m` (default) or `1h`.",
				Validators:          []validator.String{stringvalidator.OneOf("5m", "1h")},
			},
		},
	}
}

func (r *MessageResource) ConfigValidators(_ context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{messageConfigValidator{}}
}

func (r *MessageResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	pd, ok := req.ProviderData.(*providerdata.ProviderData)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *providerdata.ProviderData, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}

	if !providerrors.RequireResourceAPIClient(pd.Client, &resp.Diagnostics) {
		return
	}

	r.client = pd.Client
}

func (r *MessageResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data MessageResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	params, diags := buildMessageParams(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	message, err := r.client.Messages.New(ctx, params)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create message: %s", err))
		return
	}

	mapMessageToState(message, &data)

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

// cacheControlParam maps a cache_control block to the SDK type. A block with
// no ttl still yields a marker. The SDK omits a zero-valued struct (omitzero),
// so the constant `type` is set explicitly to keep `type: ephemeral` in the body.
func cacheControlParam(m *CacheControlModel) anthropic.CacheControlEphemeralParam {
	p := anthropic.CacheControlEphemeralParam{Type: "ephemeral"}
	if m != nil && isKnownString(m.TTL) {
		p.TTL = anthropic.CacheControlEphemeralTTL(m.TTL.ValueString())
	}
	return p
}

func isKnownString(v types.String) bool { return !v.IsNull() && !v.IsUnknown() }

// buildMessageParams turns the planned model into Messages API parameters.
func buildMessageParams(ctx context.Context, data *MessageResourceModel) (anthropic.MessageNewParams, diag.Diagnostics) {
	var diags diag.Diagnostics

	var msgModels []MessageResourceParamModel
	diags.Append(data.Messages.ElementsAs(ctx, &msgModels, false)...)
	if diags.HasError() {
		return anthropic.MessageNewParams{}, diags
	}

	sdkMessages := make([]anthropic.MessageParam, len(msgModels))
	for i, m := range msgModels {
		block := anthropic.NewTextBlock(m.Content.ValueString())
		if m.CacheControl != nil {
			block.OfText.CacheControl = cacheControlParam(m.CacheControl)
		}
		switch m.Role.ValueString() {
		case "user":
			sdkMessages[i] = anthropic.NewUserMessage(block)
		case "assistant":
			sdkMessages[i] = anthropic.NewAssistantMessage(block)
		default:
			diags.AddError(
				"Invalid Message Role",
				fmt.Sprintf("Message at index %d has invalid role %q; must be \"user\" or \"assistant\".", i, m.Role.ValueString()),
			)
			return anthropic.MessageNewParams{}, diags
		}
	}

	params := anthropic.MessageNewParams{
		Model:     data.Model.ValueString(),
		MaxTokens: data.MaxTokens.ValueInt64(),
		Messages:  sdkMessages,
	}

	if isKnownString(data.System) {
		params.System = []anthropic.TextBlockParam{{Text: data.System.ValueString()}}
	}

	if !data.Temperature.IsNull() && !data.Temperature.IsUnknown() {
		params.Temperature = anthropic.Float(data.Temperature.ValueFloat64())
	}

	if !data.StopSequences.IsNull() && !data.StopSequences.IsUnknown() {
		var seqs []string
		diags.Append(data.StopSequences.ElementsAs(ctx, &seqs, false)...)
		params.StopSequences = seqs
	}

	if data.Metadata != nil && isKnownString(data.Metadata.UserID) {
		params.Metadata = anthropic.MetadataParam{UserID: anthropic.String(data.Metadata.UserID.ValueString())}
	}

	if isKnownString(data.ServiceTier) {
		params.ServiceTier = anthropic.MessageNewParamsServiceTier(data.ServiceTier.ValueString())
	}

	if isKnownString(data.InferenceGeo) {
		params.InferenceGeo = anthropic.String(data.InferenceGeo.ValueString())
	}

	if data.Thinking != nil {
		display := data.Thinking.Display.ValueString()
		switch data.Thinking.Type.ValueString() {
		case "enabled":
			params.Thinking = anthropic.ThinkingConfigParamUnion{OfEnabled: &anthropic.ThinkingConfigEnabledParam{
				BudgetTokens: data.Thinking.BudgetTokens.ValueInt64(),
				Display:      anthropic.ThinkingConfigEnabledDisplay(display),
			}}
		case "adaptive":
			params.Thinking = anthropic.ThinkingConfigParamUnion{OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{
				Display: anthropic.ThinkingConfigAdaptiveDisplay(display),
			}}
		case "disabled":
			params.Thinking = anthropic.ThinkingConfigParamUnion{OfDisabled: &anthropic.ThinkingConfigDisabledParam{}}
		default:
			diags.AddAttributeError(path.Root("thinking").AtName("type"), "Invalid Thinking Type",
				fmt.Sprintf("Unsupported thinking type %q.", data.Thinking.Type.ValueString()))
		}
	}

	if data.OutputConfig != nil {
		oc := anthropic.OutputConfigParam{}
		if isKnownString(data.OutputConfig.Effort) {
			oc.Effort = anthropic.OutputConfigEffort(data.OutputConfig.Effort.ValueString())
		}
		if !data.OutputConfig.Format.IsNull() && !data.OutputConfig.Format.IsUnknown() {
			var schemaObj map[string]any
			if err := json.Unmarshal([]byte(data.OutputConfig.Format.ValueString()), &schemaObj); err != nil {
				diags.AddAttributeError(path.Root("output_config").AtName("format"), "Invalid JSON Schema",
					fmt.Sprintf("output_config.format must be a JSON object holding a JSON schema: %s", err))
			} else {
				oc.Format = anthropic.JSONOutputFormatParam{Schema: schemaObj}
			}
		}
		params.OutputConfig = oc
	}

	if data.CacheControl != nil {
		params.CacheControl = cacheControlParam(data.CacheControl)
	}

	return params, diags
}

// mapMessageToState copies the API response into the computed attributes.
func mapMessageToState(message *anthropic.Message, data *MessageResourceModel) {
	data.ID = types.StringValue(message.ID)
	data.StopReason = types.StringValue(string(message.StopReason))
	data.StopSequence = tfvalue.StringOrNull(message.StopSequence)
	data.InputTokens = types.Int64Value(message.Usage.InputTokens)
	data.OutputTokens = types.Int64Value(message.Usage.OutputTokens)
	data.ThinkingTokens = types.Int64Value(message.Usage.OutputTokensDetails.ThinkingTokens)
	data.CacheCreationInputTokens = types.Int64Value(message.Usage.CacheCreationInputTokens)
	data.CacheReadInputTokens = types.Int64Value(message.Usage.CacheReadInputTokens)
	data.UsageServiceTier = tfvalue.StringOrNull(string(message.Usage.ServiceTier))
	data.UsageInferenceGeo = tfvalue.StringOrNull(message.Usage.InferenceGeo)

	data.StopDetails = types.ObjectNull(stopDetailsAttrTypes)
	if message.JSON.StopDetails.Valid() && message.StopDetails.Category != "" {
		data.StopDetails = types.ObjectValueMust(stopDetailsAttrTypes, map[string]attr.Value{
			"category":    types.StringValue(string(message.StopDetails.Category)),
			"explanation": types.StringValue(message.StopDetails.Explanation),
		})
	}

	var parts []string
	for _, block := range message.Content {
		if block.Type == "text" {
			parts = append(parts, block.Text)
		}
	}
	data.Content = types.StringValue(strings.Join(parts, ""))
}

// messageConfigValidator enforces cross-attribute rules that depend on
// Terraform config values. Unknown values count as neither set nor missing.
type messageConfigValidator struct{}

func (messageConfigValidator) Description(context.Context) string {
	return "validates thinking budget/display combinations"
}

func (v messageConfigValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (messageConfigValidator) ValidateResource(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var thinking types.Object
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("thinking"), &thinking)...)
	if !resp.Diagnostics.HasError() && !thinking.IsNull() && !thinking.IsUnknown() {
		attrs := thinking.Attributes()
		typ, _ := attrs["type"].(types.String)
		budget, _ := attrs["budget_tokens"].(types.Int64)
		display, _ := attrs["display"].(types.String)
		base := path.Root("thinking")
		if isKnownString(typ) {
			budgetSet := !budget.IsNull() && !budget.IsUnknown()
			budgetMissing := budget.IsNull() && !budget.IsUnknown()
			switch typ.ValueString() {
			case "enabled":
				if budgetMissing {
					resp.Diagnostics.AddAttributeError(base.AtName("budget_tokens"), "Missing budget_tokens",
						`thinking.budget_tokens is required when thinking.type is "enabled".`)
				}
			default:
				if budgetSet {
					resp.Diagnostics.AddAttributeError(base.AtName("budget_tokens"), "Unexpected budget_tokens",
						fmt.Sprintf("thinking.budget_tokens is only allowed when thinking.type is \"enabled\", got %q.", typ.ValueString()))
				}
			}
			if typ.ValueString() == "disabled" && isKnownString(display) {
				resp.Diagnostics.AddAttributeError(base.AtName("display"), "Unexpected display",
					`thinking.display is not allowed when thinking.type is "disabled".`)
			}
		}
	}
}
