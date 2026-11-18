// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package models

import (
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
)

// ModelModel holds the attributes of one model, as the anthropic_models list
// exposes it and as anthropic_model exposes it next to model_id.
type ModelModel struct {
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

var capabilitiesAttrTypes = map[string]attr.Type{
	"batch":              types.BoolType,
	"citations":          types.BoolType,
	"code_execution":     types.BoolType,
	"context_management": types.ObjectType{AttrTypes: contextManagementAttrTypes},
	"effort":             types.ObjectType{AttrTypes: effortAttrTypes},
	"image_input":        types.BoolType,
	"pdf_input":          types.BoolType,
	"server_tools":       types.ObjectType{AttrTypes: serverToolsAttrTypes},
	"structured_outputs": types.BoolType,
	"thinking":           types.BoolType,
	"thinking_types":     types.ObjectType{AttrTypes: thinkingTypesAttrTypes},
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

var serverToolsAttrTypes = map[string]attr.Type{
	"code_execution": types.BoolType,
	"web_search":     types.BoolType,
}

var thinkingTypesAttrTypes = map[string]attr.Type{
	"adaptive":      types.BoolType,
	"between_tools": types.BoolType,
	"disabled":      types.BoolType,
	"enabled":       types.BoolType,
}

// modelAttributes returns the Computed attributes describing one model,
// shared by both data sources so the two schemas cannot drift.
func modelAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
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
		"lifecycle_stage": schema.StringAttribute{
			Computed: true,
			MarkdownDescription: "The model's current lifecycle stage (the API's `lifecycle` field, renamed because " +
				"`lifecycle` is a Terraform meta-argument). `active`: available, open to new adopters and not " +
				"scheduled for retirement. `deprecated`: still callable by organizations with existing access, closed to " +
				"new adopters and headed for retirement. `retired`: no longer available, requests naming it fail.",
		},
		"deprecated_at": schema.StringAttribute{
			Computed: true,
			MarkdownDescription: "RFC 3339 datetime of the model's most recent deprecation. Set for `deprecated` and " +
				"`retired` models, null while the model is `active`.",
		},
		"retires_at": schema.StringAttribute{
			Computed: true,
			MarkdownDescription: "RFC 3339 datetime of the model's currently scheduled retirement. Null while the model is " +
				"`active` or no retirement is scheduled. The date can be revised until the retirement happens, and a past " +
				"date on a `deprecated` model means the retirement is overdue, not that it happened: `lifecycle_stage` " +
				"is the signal.",
		},
		"line": schema.StringAttribute{
			Computed: true,
			MarkdownDescription: "The model line the model belongs to, such as `opus`, `sonnet` or `haiku` (more lines may " +
				"be added). Null when the model belongs to no line; do not infer the line from the ID.",
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
					Computed: true,
					MarkdownDescription: "Whether code the model runs in the code execution tool can call the request's other " +
						"tools, as in programmatic tool calling and dynamic filtering for web search and web fetch. Support " +
						"for the code execution tool itself is `server_tools.code_execution`.",
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
				"server_tools": schema.SingleNestedAttribute{
					Computed: true,
					MarkdownDescription: "Web search and code execution server tool support. A supported tool can still be " +
						"rejected for your organization, for example when an admin has turned web search off.",
					Attributes: map[string]schema.Attribute{
						"code_execution": schema.BoolAttribute{
							Computed: true,
							MarkdownDescription: "Whether the model supports the code execution tool (at least one version of " +
								"it, not necessarily every version).",
						},
						"web_search": schema.BoolAttribute{
							Computed: true,
							MarkdownDescription: "Whether the model supports the web search tool (at least one version of it, " +
								"not necessarily every version).",
						},
					},
				},
				"structured_outputs": schema.BoolAttribute{
					Computed:            true,
					MarkdownDescription: "Whether the model supports structured output / JSON mode.",
				},
				"thinking": schema.BoolAttribute{
					Computed: true,
					MarkdownDescription: "Whether the model supports extended thinking. `thinking_types` lists the " +
						"`thinking.type` values the model accepts.",
				},
				"thinking_types": schema.SingleNestedAttribute{
					Computed: true,
					MarkdownDescription: "Which `thinking.type` values the model accepts on requests. Read each one on its " +
						"own: `enabled` can be false while `disabled` is true. A value marked false is rejected with a `400`; " +
						"one marked true can still be rejected for another reason, such as an effort level the model does " +
						"not allow with thinking off.",
					Attributes: map[string]schema.Attribute{
						"adaptive": schema.BoolAttribute{
							Computed:            true,
							MarkdownDescription: "Whether the model accepts `adaptive` (the model decides whether and how much to think).",
						},
						"between_tools": schema.BoolAttribute{
							Computed: true,
							MarkdownDescription: "Whether the model accepts `between_tools` (thinking off, and short progress " +
								"updates the model writes between tool calls come back as thinking blocks).",
						},
						"disabled": schema.BoolAttribute{
							Computed: true,
							MarkdownDescription: "Whether the model accepts `disabled` (thinking off). True on a model that does " +
								"not support thinking.",
						},
						"enabled": schema.BoolAttribute{
							Computed:            true,
							MarkdownDescription: "Whether the model accepts `enabled` (extended thinking with a caller-set `budget_tokens`).",
						},
					},
				},
			},
		},
	}
}

// mapModelInfo maps one Models API entry to its Terraform attributes. Absent
// dates and line (the API sends null) map to null.
func mapModelInfo(m *anthropic.ModelInfo) (ModelModel, diag.Diagnostics) {
	var diags diag.Diagnostics

	caps, d := modelCapabilitiesValue(m.Capabilities)
	diags.Append(d...)
	if diags.HasError() {
		return ModelModel{}, diags
	}

	return ModelModel{
		ID:             types.StringValue(m.ID),
		DisplayName:    types.StringValue(m.DisplayName),
		CreatedAt:      types.StringValue(m.CreatedAt.Format(time.RFC3339Nano)),
		LifecycleStage: tfvalue.StringOrNull(string(m.Lifecycle)),
		DeprecatedAt:   tfvalue.TimeOrNull(m.DeprecatedAt),
		RetiresAt:      tfvalue.TimeOrNull(m.RetiresAt),
		Line:           tfvalue.StringOrNull(string(m.Line)),
		MaxInputTokens: types.Int64Value(m.MaxInputTokens),
		MaxTokens:      types.Int64Value(m.MaxTokens),
		Capabilities:   caps,
	}, diags
}

func modelCapabilitiesValue(c anthropic.ModelCapabilities) (types.Object, diag.Diagnostics) {
	var diags diag.Diagnostics
	null := types.ObjectNull(capabilitiesAttrTypes)

	ctxMgmt, d := types.ObjectValue(contextManagementAttrTypes, map[string]attr.Value{
		"clear_thinking_20251015":  types.BoolValue(c.ContextManagement.ClearThinking20251015.Supported),
		"clear_tool_uses_20250919": types.BoolValue(c.ContextManagement.ClearToolUses20250919.Supported),
		"compact_20260112":         types.BoolValue(c.ContextManagement.Compact20260112.Supported),
	})
	diags.Append(d...)

	effort, d := types.ObjectValue(effortAttrTypes, map[string]attr.Value{
		"high":   types.BoolValue(c.Effort.High.Supported),
		"low":    types.BoolValue(c.Effort.Low.Supported),
		"max":    types.BoolValue(c.Effort.Max.Supported),
		"medium": types.BoolValue(c.Effort.Medium.Supported),
	})
	diags.Append(d...)

	serverTools, d := types.ObjectValue(serverToolsAttrTypes, map[string]attr.Value{
		"code_execution": types.BoolValue(c.ServerTools.CodeExecution.Supported),
		"web_search":     types.BoolValue(c.ServerTools.WebSearch.Supported),
	})
	diags.Append(d...)

	thinkingTypes, d := types.ObjectValue(thinkingTypesAttrTypes, map[string]attr.Value{
		"adaptive":      types.BoolValue(c.Thinking.Types.Adaptive.Supported),
		"between_tools": types.BoolValue(c.Thinking.Types.BetweenTools.Supported),
		"disabled":      types.BoolValue(c.Thinking.Types.Disabled.Supported),
		"enabled":       types.BoolValue(c.Thinking.Types.Enabled.Supported),
	})
	diags.Append(d...)
	if diags.HasError() {
		return null, diags
	}

	caps, d := types.ObjectValue(capabilitiesAttrTypes, map[string]attr.Value{
		"batch":              types.BoolValue(c.Batch.Supported),
		"citations":          types.BoolValue(c.Citations.Supported),
		"code_execution":     types.BoolValue(c.CodeExecution.Supported),
		"context_management": ctxMgmt,
		"effort":             effort,
		"image_input":        types.BoolValue(c.ImageInput.Supported),
		"pdf_input":          types.BoolValue(c.PDFInput.Supported),
		"server_tools":       serverTools,
		"structured_outputs": types.BoolValue(c.StructuredOutputs.Supported),
		"thinking":           types.BoolValue(c.Thinking.Supported),
		"thinking_types":     thinkingTypes,
	})
	diags.Append(d...)
	if diags.HasError() {
		return null, diags
	}
	return caps, diags
}
