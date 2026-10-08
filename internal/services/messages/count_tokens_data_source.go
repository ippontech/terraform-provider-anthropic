// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package messages

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// objectAsOptions lets types.Object.As accept unknown/null leaf values.
var objectAsOptions = basetypes.ObjectAsOptions{UnhandledNullAsEmpty: true, UnhandledUnknownAsEmpty: true}

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &CountTokensDataSource{}
var _ datasource.DataSourceWithConfigure = &CountTokensDataSource{}
var _ datasource.DataSourceWithConfigValidators = &CountTokensDataSource{}

func NewCountTokensDataSource() datasource.DataSource {
	return &CountTokensDataSource{}
}

// CountTokensDataSource defines the data source implementation.
type CountTokensDataSource struct {
	client *anthropic.Client
}

// CountTokensDataSourceModel describes the data source data model.
type CountTokensDataSourceModel struct {
	Model        types.String `tfsdk:"model"`
	Messages     types.List   `tfsdk:"messages"`
	System       types.String `tfsdk:"system"`
	Thinking     types.Object `tfsdk:"thinking"`
	OutputConfig types.Object `tfsdk:"output_config"`
	CacheControl types.Object `tfsdk:"cache_control"`
	InputTokens  types.Int64  `tfsdk:"input_tokens"`
}

// countTokensThinkingModel is the nested `thinking` block.
type countTokensThinkingModel struct {
	Type         types.String `tfsdk:"type"`
	BudgetTokens types.Int64  `tfsdk:"budget_tokens"`
	Display      types.String `tfsdk:"display"`
}

// countTokensOutputConfigModel is the nested `output_config` block.
type countTokensOutputConfigModel struct {
	Effort types.String         `tfsdk:"effort"`
	Format jsontypes.Normalized `tfsdk:"format"`
}

// countTokensCacheControlModel is the nested `cache_control` block.
type countTokensCacheControlModel struct {
	TTL types.String `tfsdk:"ttl"`
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
			"thinking": schema.SingleNestedAttribute{
				Optional: true,
				MarkdownDescription: "Extended thinking configuration, so the count includes the thinking overhead the real request would carry. " +
					"Probed on `POST /v1/messages/count_tokens` on 2026-10-08: `type = \"disabled\"` is accepted for `claude-fable-5-1` and `claude-opus-5-5`, and rejected with a 400 for `claude-sonnet-5-5` (the API asks for `between_tools` instead, which this provider does not support). Other models were not verified.",
				Attributes: map[string]schema.Attribute{
					"type": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "Thinking mode: `enabled` (requires `budget_tokens`), `disabled` or `adaptive`.",
						Validators:          []validator.String{stringvalidator.OneOf("enabled", "disabled", "adaptive")},
					},
					"budget_tokens": schema.Int64Attribute{
						Optional:            true,
						MarkdownDescription: "Maximum number of tokens the model may use for internal reasoning. Must be at least `1024` and less than the request's `max_tokens`. Required when `type` is `enabled`, not allowed otherwise.",
						Validators:          []validator.Int64{int64validator.AtLeast(1024)},
					},
					"display": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "How thinking content appears in the response: `summarized` or `omitted`. Not allowed when `type` is `disabled`.",
						Validators:          []validator.String{stringvalidator.OneOf("summarized", "omitted")},
					},
				},
			},
			"output_config": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Output configuration (effort level and structured-output format) that the real request would carry.",
				Attributes: map[string]schema.Attribute{
					"effort": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "Effort level: `low`, `medium`, `high`, `xhigh` or `max`. Not every model supports it (the API answers 400 otherwise).",
						Validators:          []validator.String{stringvalidator.OneOf("low", "medium", "high", "xhigh", "max")},
					},
					"format": schema.StringAttribute{
						Optional:            true,
						CustomType:          jsontypes.NormalizedType{},
						MarkdownDescription: "JSON schema (a JSON object, typically from `jsonencode()`) that constrains the response, sent as a `json_schema` output format.",
					},
				},
			},
			"cache_control": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Top-level automatic prompt-caching breakpoint that the real request would carry.",
				Attributes: map[string]schema.Attribute{
					"ttl": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "Time-to-live of the breakpoint: `5m` (API default) or `1h`.",
						Validators:          []validator.String{stringvalidator.OneOf("5m", "1h")},
					},
				},
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

func (d *CountTokensDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data CountTokensDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	params, diags := buildCountTokensParams(ctx, data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	result, err := d.client.Messages.CountTokens(ctx, params)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to count tokens: %s", err))
		return
	}

	data.InputTokens = types.Int64Value(result.InputTokens)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// ConfigValidators enforces the cross-field rules of the `thinking` block.
func (d *CountTokensDataSource) ConfigValidators(_ context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{countTokensThinkingValidator{}}
}

// countTokensThinkingValidator requires budget_tokens iff type is "enabled"
// and forbids display with type "disabled". Unknown values count as neither
// set nor missing, so configs fed by unresolved references still validate.
type countTokensThinkingValidator struct{}

func (countTokensThinkingValidator) Description(_ context.Context) string {
	return "budget_tokens is required when thinking.type is enabled (and not allowed otherwise); display is not allowed when thinking.type is disabled"
}

func (v countTokensThinkingValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (countTokensThinkingValidator) ValidateDataSource(ctx context.Context, req datasource.ValidateConfigRequest, resp *datasource.ValidateConfigResponse) {
	var obj types.Object
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("thinking"), &obj)...)
	if resp.Diagnostics.HasError() || obj.IsNull() || obj.IsUnknown() {
		return
	}
	var m countTokensThinkingModel
	resp.Diagnostics.Append(obj.As(ctx, &m, objectAsOptions)...)
	if resp.Diagnostics.HasError() || m.Type.IsUnknown() || m.Type.IsNull() {
		return
	}

	base := path.Root("thinking")
	budgetSet := !m.BudgetTokens.IsNull() && !m.BudgetTokens.IsUnknown()
	budgetMissing := m.BudgetTokens.IsNull() && !m.BudgetTokens.IsUnknown()
	displaySet := !m.Display.IsNull() && !m.Display.IsUnknown()

	switch m.Type.ValueString() {
	case "enabled":
		if budgetMissing {
			resp.Diagnostics.AddAttributeError(base.AtName("budget_tokens"), "Missing budget_tokens",
				"`budget_tokens` is required when `thinking.type` is \"enabled\".")
		}
	case "disabled":
		if displaySet {
			resp.Diagnostics.AddAttributeError(base.AtName("display"), "Invalid display",
				"`display` is not allowed when `thinking.type` is \"disabled\".")
		}
		if budgetSet {
			resp.Diagnostics.AddAttributeError(base.AtName("budget_tokens"), "Invalid budget_tokens",
				"`budget_tokens` is only allowed when `thinking.type` is \"enabled\".")
		}
	default: // adaptive
		if budgetSet {
			resp.Diagnostics.AddAttributeError(base.AtName("budget_tokens"), "Invalid budget_tokens",
				"`budget_tokens` is only allowed when `thinking.type` is \"enabled\".")
		}
	}
}

// buildCountTokensParams converts the data source model into SDK request params.
func buildCountTokensParams(ctx context.Context, data CountTokensDataSourceModel) (anthropic.MessageCountTokensParams, diag.Diagnostics) {
	var diags diag.Diagnostics

	var msgModels []MessageParamModel
	diags.Append(data.Messages.ElementsAs(ctx, &msgModels, false)...)
	if diags.HasError() {
		return anthropic.MessageCountTokensParams{}, diags
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
			diags.AddError(
				"Invalid Message Role",
				fmt.Sprintf("Message at index %d has invalid role %q; must be \"user\" or \"assistant\".", i, m.Role.ValueString()),
			)
			return anthropic.MessageCountTokensParams{}, diags
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

	if !data.Thinking.IsNull() && !data.Thinking.IsUnknown() {
		var t countTokensThinkingModel
		diags.Append(data.Thinking.As(ctx, &t, objectAsOptions)...)
		if diags.HasError() {
			return params, diags
		}
		switch t.Type.ValueString() {
		case "enabled":
			cfg := anthropic.ThinkingConfigEnabledParam{BudgetTokens: t.BudgetTokens.ValueInt64()}
			if !t.Display.IsNull() {
				cfg.Display = anthropic.ThinkingConfigEnabledDisplay(t.Display.ValueString())
			}
			params.Thinking = anthropic.ThinkingConfigParamUnion{OfEnabled: &cfg}
		case "disabled":
			params.Thinking = anthropic.ThinkingConfigParamUnion{OfDisabled: &anthropic.ThinkingConfigDisabledParam{}}
		case "adaptive":
			cfg := anthropic.ThinkingConfigAdaptiveParam{}
			if !t.Display.IsNull() {
				cfg.Display = anthropic.ThinkingConfigAdaptiveDisplay(t.Display.ValueString())
			}
			params.Thinking = anthropic.ThinkingConfigParamUnion{OfAdaptive: &cfg}
		default:
			diags.AddAttributeError(path.Root("thinking").AtName("type"), "Invalid thinking type",
				fmt.Sprintf("Unsupported thinking type %q.", t.Type.ValueString()))
			return params, diags
		}
	}

	if !data.OutputConfig.IsNull() && !data.OutputConfig.IsUnknown() {
		var o countTokensOutputConfigModel
		diags.Append(data.OutputConfig.As(ctx, &o, objectAsOptions)...)
		if diags.HasError() {
			return params, diags
		}
		if !o.Effort.IsNull() {
			params.OutputConfig.Effort = anthropic.OutputConfigEffort(o.Effort.ValueString())
		}
		if !o.Format.IsNull() && !o.Format.IsUnknown() {
			var schemaObj map[string]any
			if err := json.Unmarshal([]byte(o.Format.ValueString()), &schemaObj); err != nil || schemaObj == nil {
				diags.AddAttributeError(path.Root("output_config").AtName("format"), "Invalid format",
					fmt.Sprintf("`format` must be a JSON object holding a JSON schema: %v", err))
				return params, diags
			}
			params.OutputConfig.Format = anthropic.JSONOutputFormatParam{Schema: schemaObj}
		}
	}

	if !data.CacheControl.IsNull() && !data.CacheControl.IsUnknown() {
		var c countTokensCacheControlModel
		diags.Append(data.CacheControl.As(ctx, &c, objectAsOptions)...)
		if diags.HasError() {
			return params, diags
		}
		cc := anthropic.CacheControlEphemeralParam{}
		if !c.TTL.IsNull() {
			cc.TTL = anthropic.CacheControlEphemeralTTL(c.TTL.ValueString())
		}
		params.CacheControl = cc
	}

	return params, diags
}
