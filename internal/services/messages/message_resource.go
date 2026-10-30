// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package messages

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
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
var _ resource.ResourceWithModifyPlan = &MessageResource{}

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
	Speed                    types.String       `tfsdk:"speed"`
	Thinking                 *ThinkingModel     `tfsdk:"thinking"`
	OutputConfig             *OutputConfigModel `tfsdk:"output_config"`
	CacheControl             *CacheControlModel `tfsdk:"cache_control"`
	Diagnostics              *DiagnosticsModel  `tfsdk:"diagnostics"`
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
	UsageSpeed               types.String       `tfsdk:"usage_speed"`
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

// temperatureDeprecationMessage is the warning Terraform prints when a
// configuration sets temperature. API version 2026-10-21 (Anthropic Go SDK v2)
// rejects the parameter at any value, so it goes with that migration in the
// next major version. There is deliberately no "only 1.0" validator: under
// 2023-06-01, which SDK v1 sends, models older than Claude 4.7 still accept
// other values, and rejecting them at plan time would break working configs.
const temperatureDeprecationMessage = "temperature is deprecated and will be removed in the next major version of the provider, " +
	"together with the move to Anthropic Go SDK v2 and API version 2026-10-21, which rejects temperature (HTTP 400) at any value, 1.0 included. " +
	"Claude 4.7 and later models already reject any value other than 1.0. " +
	"Remove the attribute: the request then omits it and the model samples at its default temperature of 1.0."

// stopDetailsAttrTypes is the object type of the computed stop_details attribute.
var stopDetailsAttrTypes = map[string]attr.Type{
	"category":    types.StringType,
	"explanation": types.StringType,
}

// DiagnosticsModel mirrors the API's cache diagnostics: previous_message_id
// goes into the request's diagnostics object, cache_miss_reason comes from
// the response's.
type DiagnosticsModel struct {
	PreviousMessageID types.String `tfsdk:"previous_message_id"`
	CacheMissReason   types.Object `tfsdk:"cache_miss_reason"`
}

// cacheMissReasonAttrTypes is the object type of diagnostics.cache_miss_reason.
var cacheMissReasonAttrTypes = map[string]attr.Type{
	"type":                      types.StringType,
	"cache_missed_input_tokens": types.Int64Type,
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
				MarkdownDescription: "Amount of randomness injected into the response (0.0–1.0). Will be removed in the next major version, together with the move to API version `2026-10-21`, which rejects `temperature` (HTTP 400) at any value, `1.0` included. Under the API version the provider sends today (`2023-06-01`), Claude 4.7 and later models already reject any value other than `1.0`. If not specified, the parameter is not sent and the model samples at its default (`1.0`).",
				DeprecationMessage:  temperatureDeprecationMessage,
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
			"speed": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: speedDescription,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
				Validators:          []validator.String{stringvalidator.OneOf("standard", "fast")},
			},
			"thinking": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Extended thinking configuration. If omitted, the model's default behaviour applies. Note: `{ type = \"disabled\" }` is rejected by the API (HTTP 400) on claude-fable-5-1, claude-opus-5-5, and claude-sonnet-5-5 (verified 2026-10-08); omit the block instead, use `type = \"between_tools\"` to turn thinking off on a model that accepts it, or use `type = \"adaptive\"` with `output_config.effort` to control thinking behavior.",
				PlanModifiers:       []planmodifier.Object{objectplanmodifier.RequiresReplace()},
				Attributes: map[string]schema.Attribute{
					"type": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "Thinking mode: `enabled` (requires `budget_tokens`), `adaptive` (the model decides), `disabled` or `between_tools` (thinking off, but short progress updates the model writes between tool calls come back as thinking blocks; some models that refuse `disabled` accept it). `adaptive` is not supported by every model (`claude-haiku-4-5-20251001` rejects it). Thinking content is never exposed in `content`; `thinking_tokens` shows its cost.",
						Validators:          []validator.String{stringvalidator.OneOf("enabled", "disabled", "adaptive", "between_tools")},
					},
					"budget_tokens": schema.Int64Attribute{
						Optional:            true,
						MarkdownDescription: "Maximum number of tokens the model may spend on thinking (at least `1024`, and lower than `max_tokens`). Required when `type` is `enabled`, not allowed otherwise.",
						Validators:          []validator.Int64{int64validator.AtLeast(1024)},
					},
					"display": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "How thinking content is returned: `summarized` or `omitted`. Only allowed when `type` is `enabled` or `adaptive`.",
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
			"diagnostics":   diagnosticsAttribute(),
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
			"usage_speed": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The speed the request was actually served at (`standard` or `fast`), when reported.",
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

// speedDescription documents `speed`, shared with anthropic_count_tokens.
const speedDescription = "Inference speed: `standard` or `fast`. `fast` (fast mode) gives higher output tokens per second at " +
	"premium pricing on supported models only; other models answer HTTP 400. Claude Opus 5.5 needs no beta header; models " +
	"where fast mode is in research preview (Claude Opus 5, Claude Opus 4.8) require the `fast-mode-2026-02-01` beta header " +
	"for any `speed` value, which this provider does not send, so they answer HTTP 400. Requests at different speeds do not " +
	"share cached prompt prefixes. If omitted, the parameter is not sent."

// diagnosticsAttribute builds the `diagnostics` block: the request's
// previous_message_id and, Computed, the response's cache_miss_reason.
func diagnosticsAttribute() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Optional: true,
		MarkdownDescription: "Cache diagnostics: asks the API why the prompt cache could not reuse the prefix of a previous " +
			"request. The block is the opt-in, and the API keeps a short-lived fingerprint only of requests that include " +
			"it, so set it on every turn: `{}` on the first one, then `previous_message_id` set to the previous turn's `id`. " +
			"See [cache diagnostics](https://platform.claude.com/docs/en/build-with-claude/cache-diagnostics).",
		PlanModifiers: []planmodifier.Object{objectplanmodifier.RequiresReplace()},
		Attributes: map[string]schema.Attribute{
			"previous_message_id": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "The `id` (`msg_...`) of the previous request to compare this one with, typically " +
					"another `anthropic_message`'s `id`. Omit it on the first turn: the request then sends " +
					"`previous_message_id: null`, which opts in without a comparison.",
				Validators: []validator.String{stringvalidator.LengthBetween(1, 256)},
			},
			"cache_miss_reason": schema.SingleNestedAttribute{
				Computed: true,
				MarkdownDescription: "The first point where this request diverged from the previous one, as reported by " +
					"the API. Null when it reported none: on the first turn, when no divergence was found, or when the " +
					"comparison was still running when the response was sent (inconclusive).",
				PlanModifiers: []planmodifier.Object{objectplanmodifier.UseStateForUnknown()},
				Attributes: map[string]schema.Attribute{
					"type": schema.StringAttribute{
						Computed: true,
						MarkdownDescription: "`model_changed` (the `model` or `speed` differs), `system_changed`, " +
							"`tools_changed`, `messages_changed` (an earlier message was changed instead of only appended " +
							"to), `previous_message_not_found` (no stored record of `previous_message_id`; not evidence of a " +
							"change) or `unavailable` (no diagnosis, for example because `thinking` or `output_config` differ).",
					},
					"cache_missed_input_tokens": schema.Int64Attribute{
						Computed: true,
						MarkdownDescription: "Approximate number of input tokens that would have been read from the cache " +
							"had the prefix matched (the `*_changed` types only, null otherwise). A magnitude, not a " +
							"billing number.",
					},
				},
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

// ModifyPlan rejects an output_config.format that is not a JSON object when
// the plan sends the message: a create, including the create half of a
// replacement, which Terraform plans with a null prior state. A message
// already in state is left alone, so one created while a null format was
// still dropped from the request keeps planning with no change. An unknown
// format is checked by Create.
func (r *MessageResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if !req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}
	var format jsontypes.Normalized
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("output_config").AtName("format"), &format)...)
	if resp.Diagnostics.HasError() || format.IsNull() || format.IsUnknown() {
		return
	}
	if _, err := decodeFormatSchema(format); err != nil {
		addInvalidFormatError(&resp.Diagnostics, err)
	}
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

// isKnown reports whether v is neither null nor unknown.
func isKnown(v attr.Value) bool { return !v.IsNull() && !v.IsUnknown() }

// textBlock returns the message's role and its text block.
func (m MessageParamModel) textBlock() (string, anthropic.ContentBlockParamUnion) {
	return m.Role.ValueString(), anthropic.NewTextBlock(m.Content.ValueString())
}

// textBlock returns the message's role and its text block, with the
// message's cache_control breakpoint when it has one.
func (m MessageResourceParamModel) textBlock() (string, anthropic.ContentBlockParamUnion) {
	block := anthropic.NewTextBlock(m.Content.ValueString())
	if m.CacheControl != nil {
		block.OfText.CacheControl = cacheControlParam(m.CacheControl)
	}
	return m.Role.ValueString(), block
}

// messageEntry is an entry of a `messages` list: MessageParamModel for
// anthropic_count_tokens, MessageResourceParamModel for anthropic_message.
type messageEntry interface {
	textBlock() (string, anthropic.ContentBlockParamUnion)
}

// decodeMessages reads a `messages` list into entries of type T and builds
// one single-block message per entry. The first entry with a role other
// than user or assistant is an error, and no message is returned.
func decodeMessages[T messageEntry](ctx context.Context, list types.List) ([]anthropic.MessageParam, diag.Diagnostics) {
	var entries []T
	diags := list.ElementsAs(ctx, &entries, false)
	if diags.HasError() {
		return nil, diags
	}
	messages := make([]anthropic.MessageParam, len(entries))
	for i, entry := range entries {
		role, block := entry.textBlock()
		switch role {
		case "user":
			messages[i] = anthropic.NewUserMessage(block)
		case "assistant":
			messages[i] = anthropic.NewAssistantMessage(block)
		default:
			diags.AddError(
				"Invalid Message Role",
				fmt.Sprintf("Message at index %d has invalid role %q; must be \"user\" or \"assistant\".", i, role),
			)
			return nil, diags
		}
	}
	return messages, diags
}

// thinkingParam maps a thinking block to the SDK union: budget_tokens goes
// with "enabled" only, display with "enabled" and "adaptive" (left out when
// null). ok is false for an unsupported type, with a zero union.
func thinkingParam(typ string, budget types.Int64, display types.String) (anthropic.ThinkingConfigParamUnion, bool) {
	switch typ {
	case "enabled":
		return anthropic.ThinkingConfigParamUnion{OfEnabled: &anthropic.ThinkingConfigEnabledParam{
			BudgetTokens: budget.ValueInt64(),
			Display:      anthropic.ThinkingConfigEnabledDisplay(display.ValueString()),
		}}, true
	case "adaptive":
		return anthropic.ThinkingConfigParamUnion{OfAdaptive: &anthropic.ThinkingConfigAdaptiveParam{
			Display: anthropic.ThinkingConfigAdaptiveDisplay(display.ValueString()),
		}}, true
	case "disabled":
		return anthropic.ThinkingConfigParamUnion{OfDisabled: &anthropic.ThinkingConfigDisabledParam{}}, true
	case "between_tools":
		betweenTools := anthropic.NewThinkingConfigBetweenToolsParam()
		return anthropic.ThinkingConfigParamUnion{OfBetweenTools: &betweenTools}, true
	default:
		return anthropic.ThinkingConfigParamUnion{}, false
	}
}

// formatSchema decodes output_config.format, a JSON schema as a JSON string.
func formatSchema(format jsontypes.Normalized) (map[string]any, error) {
	var schemaObj map[string]any
	err := json.Unmarshal([]byte(format.ValueString()), &schemaObj)
	return schemaObj, err
}

// buildMessageParams turns the planned model into Messages API parameters.
// A message that cannot be built stops it at once; the other errors
// accumulate.
func buildMessageParams(ctx context.Context, data *MessageResourceModel) (anthropic.MessageNewParams, diag.Diagnostics) {
	sdkMessages, diags := decodeMessages[MessageResourceParamModel](ctx, data.Messages)
	if diags.HasError() {
		return anthropic.MessageNewParams{}, diags
	}

	params := anthropic.MessageNewParams{
		Model:     data.Model.ValueString(),
		MaxTokens: data.MaxTokens.ValueInt64(),
		Messages:  sdkMessages,
	}

	if isKnownString(data.System) {
		params.System = []anthropic.TextBlockParam{{Text: data.System.ValueString()}}
	}

	// Deprecated (see temperatureDeprecationMessage): sent only when set, so an
	// unset temperature never reaches the API, not even as 0.
	if isKnown(data.Temperature) {
		params.Temperature = anthropic.Float(data.Temperature.ValueFloat64())
	}

	if isKnown(data.StopSequences) {
		var seqs []string
		diags.Append(data.StopSequences.ElementsAs(ctx, &seqs, false)...)
		params.StopSequences = seqs
	}

	setMessageServiceOptions(&params, data)

	if data.Thinking != nil {
		thinking, ok := thinkingParam(data.Thinking.Type.ValueString(), data.Thinking.BudgetTokens, data.Thinking.Display)
		if !ok {
			diags.AddAttributeError(path.Root("thinking").AtName("type"), "Invalid Thinking Type",
				fmt.Sprintf("Unsupported thinking type %q.", data.Thinking.Type.ValueString()))
		}
		params.Thinking = thinking
	}

	if data.OutputConfig != nil {
		outputConfig, d := messageOutputConfigParam(data.OutputConfig)
		diags.Append(d...)
		params.OutputConfig = outputConfig
	}

	if data.CacheControl != nil {
		params.CacheControl = cacheControlParam(data.CacheControl)
	}

	if data.Diagnostics != nil {
		params.Diagnostics = diagnosticsParam(data.Diagnostics)
	}

	return params, diags
}

// setMessageServiceOptions sets the known metadata.user_id, service_tier,
// inference_geo and speed.
func setMessageServiceOptions(params *anthropic.MessageNewParams, data *MessageResourceModel) {
	if data.Metadata != nil && isKnownString(data.Metadata.UserID) {
		params.Metadata = anthropic.MetadataParam{UserID: anthropic.String(data.Metadata.UserID.ValueString())}
	}
	if isKnownString(data.ServiceTier) {
		params.ServiceTier = anthropic.MessageNewParamsServiceTier(data.ServiceTier.ValueString())
	}
	if isKnownString(data.InferenceGeo) {
		params.InferenceGeo = anthropic.String(data.InferenceGeo.ValueString())
	}
	if isKnownString(data.Speed) {
		params.Speed = anthropic.MessageNewParamsSpeed(data.Speed.ValueString())
	}
}

// messageOutputConfigParam maps output_config: the known effort, and the
// format wrapped as a json_schema output format. A format that does not
// decode is an error and is left out.
func messageOutputConfigParam(m *OutputConfigModel) (anthropic.OutputConfigParam, diag.Diagnostics) {
	var diags diag.Diagnostics
	oc := anthropic.OutputConfigParam{}
	if isKnownString(m.Effort) {
		oc.Effort = anthropic.OutputConfigEffort(m.Effort.ValueString())
	}
	if isKnown(m.Format) {
		schemaObj, err := decodeFormatSchema(m.Format)
		if err != nil {
			addInvalidFormatError(&diags, err)
		} else {
			oc.Format = anthropic.JSONOutputFormatParam{Schema: schemaObj}
		}
	}
	return oc, diags
}

// diagnosticsParam maps the diagnostics block to the request object. Without
// previous_message_id it sends an explicit `"previous_message_id": null`, the
// documented first-turn opt-in: an empty DiagnosticsParam would be dropped by
// the SDK's omitzero, and with it the opt-in.
func diagnosticsParam(m *DiagnosticsModel) anthropic.DiagnosticsParam {
	if isKnownString(m.PreviousMessageID) {
		return anthropic.DiagnosticsParam{PreviousMessageID: anthropic.String(m.PreviousMessageID.ValueString())}
	}
	return anthropic.DiagnosticsParam{PreviousMessageID: param.Null[string]()}
}

// decodeFormatSchema decodes output_config.format, which must hold a JSON
// object. JSON null decodes to a nil schema, which the SDK would leave out of
// the request together with the whole output_config, so it is an error too,
// as in anthropic_count_tokens.
func decodeFormatSchema(format jsontypes.Normalized) (map[string]any, error) {
	schemaObj, err := formatSchema(format)
	if err != nil {
		return nil, err
	}
	if schemaObj == nil {
		return nil, errors.New("got null")
	}
	return schemaObj, nil
}

// addInvalidFormatError reports a format that decodeFormatSchema rejected.
func addInvalidFormatError(diags *diag.Diagnostics, err error) {
	diags.AddAttributeError(path.Root("output_config").AtName("format"), "Invalid JSON Schema",
		fmt.Sprintf("output_config.format must be a JSON object holding a JSON schema: %s", err))
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
	data.UsageSpeed = tfvalue.StringOrNull(string(message.Usage.Speed))
	if data.Diagnostics != nil {
		data.Diagnostics.CacheMissReason = cacheMissReasonValue(message)
	}

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

// cacheMissReasonValue maps the response's diagnostics.cache_miss_reason.
// A null diagnostics (no divergence, or a first turn) and a null
// cache_miss_reason (comparison still pending) both read as null.
func cacheMissReasonValue(message *anthropic.Message) types.Object {
	if !message.JSON.Diagnostics.Valid() || !message.Diagnostics.JSON.CacheMissReason.Valid() {
		return types.ObjectNull(cacheMissReasonAttrTypes)
	}
	reason := message.Diagnostics.CacheMissReason
	missed := types.Int64Null()
	if reason.JSON.CacheMissedInputTokens.Valid() {
		missed = types.Int64Value(reason.CacheMissedInputTokens)
	}
	return types.ObjectValueMust(cacheMissReasonAttrTypes, map[string]attr.Value{
		"type":                      tfvalue.StringOrNull(reason.Type),
		"cache_missed_input_tokens": missed,
	})
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
	if resp.Diagnostics.HasError() || !isKnown(thinking) {
		return
	}
	attrs := thinking.Attributes()
	typ, _ := attrs["type"].(types.String)
	if !isKnownString(typ) {
		return
	}
	budget, _ := attrs["budget_tokens"].(types.Int64)
	display, _ := attrs["display"].(types.String)
	resp.Diagnostics.Append(validateThinkingBudget(typ.ValueString(), budget)...)
	resp.Diagnostics.Append(validateThinkingDisplay(typ.ValueString(), display)...)
}

// validateThinkingBudget requires budget_tokens when the thinking type is
// "enabled" and rejects it otherwise.
func validateThinkingBudget(typ string, budget types.Int64) diag.Diagnostics {
	var diags diag.Diagnostics
	budgetPath := path.Root("thinking").AtName("budget_tokens")
	budgetSet := !budget.IsNull() && !budget.IsUnknown()
	budgetMissing := budget.IsNull() && !budget.IsUnknown()
	if typ == "enabled" && budgetMissing {
		diags.AddAttributeError(budgetPath, "Missing budget_tokens",
			`thinking.budget_tokens is required when thinking.type is "enabled".`)
	}
	if typ != "enabled" && budgetSet {
		diags.AddAttributeError(budgetPath, "Unexpected budget_tokens",
			fmt.Sprintf("thinking.budget_tokens is only allowed when thinking.type is \"enabled\", got %q.", typ))
	}
	return diags
}

// validateThinkingDisplay rejects display when thinking is off ("disabled"
// or "between_tools").
func validateThinkingDisplay(typ string, display types.String) diag.Diagnostics {
	var diags diag.Diagnostics
	if (typ == "disabled" || typ == "between_tools") && isKnownString(display) {
		diags.AddAttributeError(path.Root("thinking").AtName("display"), "Unexpected display",
			fmt.Sprintf("thinking.display is not allowed when thinking.type is %q.", typ))
	}
	return diags
}
