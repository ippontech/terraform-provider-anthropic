// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package messages

import (
	"context"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// diagStrings renders diagnostics as "severity: summary @ path", in order.
func diagStrings(diags diag.Diagnostics) []string {
	out := make([]string, 0, len(diags))
	for _, d := range diags {
		s := d.Severity().String() + ": " + d.Summary()
		if wp, ok := d.(diag.DiagnosticWithPath); ok {
			s += " @ " + wp.Path().String()
		}
		out = append(out, s)
	}
	return out
}

func userMessage(content string, cache *CacheControlModel) MessageResourceParamModel {
	return MessageResourceParamModel{Role: types.StringValue("user"), Content: types.StringValue(content), CacheControl: cache}
}

func thinkingModel(typ string, budget types.Int64, display types.String) *ThinkingModel {
	return &ThinkingModel{Type: types.StringValue(typ), BudgetTokens: budget, Display: display}
}

const minimalParams = `"model": "claude-haiku-4-5-20251001", "max_tokens": 2048,
  "messages": [{"role": "user", "content": [{"type": "text", "text": "hi"}]}]`

// TestBuildMessageParams_requestBody pins the request body built from the
// planned model for every input, the unknown values Terraform never plans
// for an input at apply time included.
func TestBuildMessageParams_requestBody(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		msgs   []MessageResourceParamModel
		change func(*MessageResourceModel)
		want   string
	}{
		"minimal": {want: `{` + minimalParams + `}`},
		"every option": {
			msgs: []MessageResourceParamModel{
				userMessage("a", &CacheControlModel{TTL: types.StringValue("1h")}),
				{Role: types.StringValue("assistant"), Content: types.StringValue("b"), CacheControl: &CacheControlModel{TTL: types.StringNull()}},
				userMessage("c", nil),
			},
			change: func(m *MessageResourceModel) {
				m.System = types.StringValue("sys")
				m.Temperature = types.Float64Value(0.25)
				m.StopSequences = types.ListValueMust(types.StringType, []attr.Value{types.StringValue("END"), types.StringValue("STOP")})
				m.Metadata = &MetadataModel{UserID: types.StringValue("user-1")}
				m.ServiceTier = types.StringValue("auto")
				m.InferenceGeo = types.StringValue("global")
				m.Speed = types.StringValue("standard")
				m.Thinking = thinkingModel("enabled", types.Int64Value(1024), types.StringValue("omitted"))
				m.OutputConfig = &OutputConfigModel{Effort: types.StringValue("max"), Format: jsontypes.NewNormalizedValue(`{"type":"object"}`)}
				m.CacheControl = &CacheControlModel{TTL: types.StringValue("5m")}
				m.Diagnostics = &DiagnosticsModel{PreviousMessageID: types.StringValue("msg_1"), CacheMissReason: types.ObjectUnknown(cacheMissReasonAttrTypes)}
			},
			want: `{"model": "claude-haiku-4-5-20251001", "max_tokens": 2048,
  "messages": [
    {"role": "user", "content": [{"type": "text", "text": "a", "cache_control": {"type": "ephemeral", "ttl": "1h"}}]},
    {"role": "assistant", "content": [{"type": "text", "text": "b", "cache_control": {"type": "ephemeral"}}]},
    {"role": "user", "content": [{"type": "text", "text": "c"}]}
  ],
  "system": [{"type": "text", "text": "sys"}], "temperature": 0.25, "stop_sequences": ["END", "STOP"],
  "metadata": {"user_id": "user-1"}, "service_tier": "auto", "inference_geo": "global", "speed": "standard",
  "thinking": {"type": "enabled", "budget_tokens": 1024, "display": "omitted"},
  "output_config": {"effort": "max", "format": {"type": "json_schema", "schema": {"type": "object"}}},
  "cache_control": {"type": "ephemeral", "ttl": "5m"}, "diagnostics": {"previous_message_id": "msg_1"}}`,
		},
		"unknown values left out": {
			change: func(m *MessageResourceModel) {
				m.System, m.ServiceTier, m.InferenceGeo, m.Speed = types.StringUnknown(), types.StringUnknown(), types.StringUnknown(), types.StringUnknown()
				m.Temperature = types.Float64Unknown()
				m.StopSequences = types.ListUnknown(types.StringType)
				m.Metadata = &MetadataModel{UserID: types.StringUnknown()}
				m.OutputConfig = &OutputConfigModel{Effort: types.StringUnknown(), Format: jsontypes.NewNormalizedUnknown()}
				m.CacheControl = &CacheControlModel{TTL: types.StringUnknown()}
				m.Diagnostics = &DiagnosticsModel{PreviousMessageID: types.StringUnknown()}
			},
			want: `{` + minimalParams + `, "cache_control": {"type": "ephemeral"}, "diagnostics": {"previous_message_id": null}}`,
		},
		"empty blocks": {
			change: func(m *MessageResourceModel) {
				m.StopSequences = types.ListValueMust(types.StringType, []attr.Value{})
				m.Metadata = &MetadataModel{UserID: types.StringNull()}
				m.OutputConfig = &OutputConfigModel{Effort: types.StringNull(), Format: jsontypes.NewNormalizedNull()}
				m.CacheControl = &CacheControlModel{TTL: types.StringNull()}
				m.Diagnostics = &DiagnosticsModel{PreviousMessageID: types.StringNull()}
			},
			want: `{` + minimalParams + `, "stop_sequences": [], "cache_control": {"type": "ephemeral"}, "diagnostics": {"previous_message_id": null}}`,
		},
		"format only": {
			change: func(m *MessageResourceModel) {
				m.OutputConfig = &OutputConfigModel{Effort: types.StringNull(), Format: jsontypes.NewNormalizedValue(`{"type":"object","required":["a"]}`)}
			},
			want: `{` + minimalParams + `, "output_config": {"format": {"type": "json_schema", "schema": {"type": "object", "required": ["a"]}}}}`,
		},
		"thinking enabled without display": {
			change: func(m *MessageResourceModel) {
				m.Thinking = thinkingModel("enabled", types.Int64Value(2048), types.StringNull())
			},
			want: `{` + minimalParams + `, "thinking": {"type": "enabled", "budget_tokens": 2048}}`,
		},
		"thinking adaptive": {
			change: func(m *MessageResourceModel) {
				m.Thinking = thinkingModel("adaptive", types.Int64Null(), types.StringNull())
			},
			want: `{` + minimalParams + `, "thinking": {"type": "adaptive"}}`,
		},
		"thinking adaptive with display": {
			change: func(m *MessageResourceModel) {
				m.Thinking = thinkingModel("adaptive", types.Int64Null(), types.StringValue("summarized"))
			},
			want: `{` + minimalParams + `, "thinking": {"type": "adaptive", "display": "summarized"}}`,
		},
		"thinking disabled ignores budget and display": {
			change: func(m *MessageResourceModel) {
				m.Thinking = thinkingModel("disabled", types.Int64Value(1024), types.StringValue("omitted"))
			},
			want: `{` + minimalParams + `, "thinking": {"type": "disabled"}}`,
		},
		"thinking between_tools ignores budget and display": {
			change: func(m *MessageResourceModel) {
				m.Thinking = thinkingModel("between_tools", types.Int64Value(1024), types.StringValue("omitted"))
			},
			want: `{` + minimalParams + `, "thinking": {"type": "between_tools"}}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			m := newTestModel(t, tc.msgs...)
			if tc.change != nil {
				tc.change(m)
			}
			params, diags := buildMessageParams(context.Background(), m)
			if diags.HasError() {
				t.Fatalf("diagnostics: %v", diags)
			}
			raw, err := params.MarshalJSON()
			if err != nil {
				t.Fatal(err)
			}
			if diff := sdktest.JSONDiff(string(raw), tc.want); diff != "" {
				t.Errorf("request body differs (got -> want):\n%s", diff)
			}
		})
	}
}

// TestBuildMessageParams_errors pins the diagnostics: an invalid role or a
// messages list that does not decode stops at once; the other errors
// accumulate.
func TestBuildMessageParams_errors(t *testing.T) {
	t.Parallel()
	invalidType := func(m *MessageResourceModel) {
		m.Thinking = thinkingModel("sometimes", types.Int64Null(), types.StringNull())
	}
	invalidFormat := func(m *MessageResourceModel) {
		m.OutputConfig = &OutputConfigModel{Effort: types.StringNull(), Format: jsontypes.NewNormalizedValue(`["a"]`)}
	}
	for name, tc := range map[string]struct {
		msgs   []MessageResourceParamModel
		change func(*MessageResourceModel)
		want   []string
	}{
		"invalid role": {
			msgs: []MessageResourceParamModel{userMessage("a", nil), {Role: types.StringValue("system"), Content: types.StringValue("b")}},
			change: func(m *MessageResourceModel) {
				invalidType(m)
				invalidFormat(m)
			},
			want: []string{"Error: Invalid Message Role"},
		},
		"messages not decodable": {
			change: func(m *MessageResourceModel) {
				m.Messages = types.ListValueMust(types.StringType, []attr.Value{types.StringValue("hi")})
				invalidType(m)
			},
			want: []string{"Error: Value Conversion Error @ [0]"},
		},
		"stop_sequences not decodable": {
			change: func(m *MessageResourceModel) {
				m.StopSequences = types.ListValueMust(types.Int64Type, []attr.Value{types.Int64Value(1)})
				invalidFormat(m)
			},
			want: []string{"Error: Value Conversion Error @ [0]", "Error: Invalid JSON Schema @ output_config.format"},
		},
		"invalid thinking type": {
			change: invalidType,
			want:   []string{"Error: Invalid Thinking Type @ thinking.type"},
		},
		"invalid format": {
			change: invalidFormat,
			want:   []string{"Error: Invalid JSON Schema @ output_config.format"},
		},
		"invalid thinking type and format": {
			change: func(m *MessageResourceModel) {
				invalidType(m)
				invalidFormat(m)
			},
			want: []string{"Error: Invalid Thinking Type @ thinking.type", "Error: Invalid JSON Schema @ output_config.format"},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			m := newTestModel(t, tc.msgs...)
			tc.change(m)
			_, diags := buildMessageParams(context.Background(), m)
			if got := diagStrings(diags); !slices.Equal(got, tc.want) {
				t.Errorf("diagnostics = %q, want %q", got, tc.want)
			}
		})
	}
}
