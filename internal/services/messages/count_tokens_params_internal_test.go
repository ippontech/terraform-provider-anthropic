// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package messages

import (
	"context"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

func ctObject(attrTypes map[string]attr.Type, values map[string]attr.Value) types.Object {
	return types.ObjectValueMust(attrTypes, values)
}

func ctThinkingValue(typ attr.Value, budget attr.Value, display attr.Value) types.Object {
	return ctObject(ctThinkingAttrTypes, map[string]attr.Value{"type": typ, "budget_tokens": budget, "display": display})
}

func ctOutputConfig(effort, format attr.Value) types.Object {
	return ctObject(ctOutputConfigAttrTypes, map[string]attr.Value{"effort": effort, "format": format})
}

// mistyped is an object whose attributes do not decode into the block's
// model, as a schema drift would give.
var mistyped = ctObject(map[string]attr.Type{"type": types.BoolType}, map[string]attr.Value{"type": types.BoolValue(true)})

const minimalCountParams = `"model": "claude-haiku-4-5-20251001",
  "messages": [{"role": "user", "content": [{"type": "text", "text": "Hello"}]}]`

// TestBuildCountTokensParams_requestBody pins the count_tokens request body
// for every input, unknown values included.
func TestBuildCountTokensParams_requestBody(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for name, tc := range map[string]struct {
		change func(*CountTokensDataSourceModel)
		want   string
	}{
		"minimal": {want: `{` + minimalCountParams + `}`},
		"every option": {
			change: func(m *CountTokensDataSourceModel) {
				m.Messages = types.ListValueMust(types.ObjectType{AttrTypes: ctMessageAttrTypes}, []attr.Value{
					ctObject(ctMessageAttrTypes, map[string]attr.Value{"role": types.StringValue("user"), "content": types.StringValue("a")}),
					ctObject(ctMessageAttrTypes, map[string]attr.Value{"role": types.StringValue("assistant"), "content": types.StringValue("b")}),
				})
				m.System = types.StringValue("sys")
				m.Speed = types.StringValue("fast")
				m.Thinking = ctThinkingValue(types.StringValue("enabled"), types.Int64Value(4096), types.StringValue("summarized"))
				m.OutputConfig = ctOutputConfig(types.StringValue("low"), jsontypes.NewNormalizedValue(`{"type":"object"}`))
				m.CacheControl = ctObject(ctCacheControlAttrTypes, map[string]attr.Value{"ttl": types.StringValue("5m")})
			},
			want: `{"model": "claude-haiku-4-5-20251001",
  "messages": [{"role": "user", "content": [{"type": "text", "text": "a"}]}, {"role": "assistant", "content": [{"type": "text", "text": "b"}]}],
  "system": "sys", "speed": "fast",
  "thinking": {"type": "enabled", "budget_tokens": 4096, "display": "summarized"},
  "output_config": {"effort": "low", "format": {"type": "json_schema", "schema": {"type": "object"}}},
  "cache_control": {"type": "ephemeral", "ttl": "5m"}}`,
		},
		"unknown blocks left out": {
			change: func(m *CountTokensDataSourceModel) {
				m.System, m.Speed = types.StringUnknown(), types.StringUnknown()
				m.Thinking = types.ObjectUnknown(ctThinkingAttrTypes)
				m.OutputConfig = types.ObjectUnknown(ctOutputConfigAttrTypes)
				m.CacheControl = types.ObjectUnknown(ctCacheControlAttrTypes)
			},
			want: `{` + minimalCountParams + `}`,
		},
		"unknown leaves left out": {
			change: func(m *CountTokensDataSourceModel) {
				m.Thinking = ctThinkingValue(types.StringValue("adaptive"), types.Int64Unknown(), types.StringUnknown())
				m.OutputConfig = ctOutputConfig(types.StringUnknown(), jsontypes.NewNormalizedUnknown())
				m.CacheControl = ctObject(ctCacheControlAttrTypes, map[string]attr.Value{"ttl": types.StringUnknown()})
			},
			want: `{` + minimalCountParams + `, "thinking": {"type": "adaptive"}, "cache_control": {"type": "ephemeral"}}`,
		},
		"empty blocks": {
			change: func(m *CountTokensDataSourceModel) {
				m.OutputConfig = ctOutputConfig(types.StringNull(), jsontypes.NewNormalizedNull())
				m.CacheControl = ctObject(ctCacheControlAttrTypes, map[string]attr.Value{"ttl": types.StringNull()})
			},
			want: `{` + minimalCountParams + `, "cache_control": {"type": "ephemeral"}}`,
		},
		"thinking enabled without display": {
			change: func(m *CountTokensDataSourceModel) {
				m.Thinking = ctThinkingValue(types.StringValue("enabled"), types.Int64Value(1024), types.StringNull())
			},
			want: `{` + minimalCountParams + `, "thinking": {"type": "enabled", "budget_tokens": 1024}}`,
		},
		"thinking adaptive with display": {
			change: func(m *CountTokensDataSourceModel) {
				m.Thinking = ctThinkingValue(types.StringValue("adaptive"), types.Int64Null(), types.StringValue("omitted"))
			},
			want: `{` + minimalCountParams + `, "thinking": {"type": "adaptive", "display": "omitted"}}`,
		},
		"thinking disabled ignores budget and display": {
			change: func(m *CountTokensDataSourceModel) {
				m.Thinking = ctThinkingValue(types.StringValue("disabled"), types.Int64Value(1024), types.StringValue("omitted"))
			},
			want: `{` + minimalCountParams + `, "thinking": {"type": "disabled"}}`,
		},
		"thinking between_tools ignores budget and display": {
			change: func(m *CountTokensDataSourceModel) {
				m.Thinking = ctThinkingValue(types.StringValue("between_tools"), types.Int64Value(1024), types.StringValue("omitted"))
			},
			want: `{` + minimalCountParams + `, "thinking": {"type": "between_tools"}}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			m := ctModel(t)
			if tc.change != nil {
				tc.change(&m)
			}
			params, diags := buildCountTokensParams(ctx, m)
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

// TestBuildCountTokensParams_errors pins the diagnostics: unlike the
// message resource, the first error stops the build.
func TestBuildCountTokensParams_errors(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	invalidType := ctThinkingValue(types.StringValue("sometimes"), types.Int64Null(), types.StringNull())
	invalidFormat := ctOutputConfig(types.StringNull(), jsontypes.NewNormalizedValue(`"text"`))
	for name, tc := range map[string]struct {
		change func(*CountTokensDataSourceModel)
		want   []string
	}{
		"invalid role": {
			change: func(m *CountTokensDataSourceModel) {
				m.Messages = types.ListValueMust(types.ObjectType{AttrTypes: ctMessageAttrTypes}, []attr.Value{
					ctObject(ctMessageAttrTypes, map[string]attr.Value{"role": types.StringValue("system"), "content": types.StringValue("a")}),
				})
				m.Thinking = invalidType
			},
			want: []string{"Error: Invalid Message Role"},
		},
		"messages not decodable": {
			change: func(m *CountTokensDataSourceModel) {
				m.Messages = types.ListValueMust(types.StringType, []attr.Value{types.StringValue("hi")})
			},
			want: []string{"Error: Value Conversion Error @ [0]"},
		},
		"thinking not decodable": {
			change: func(m *CountTokensDataSourceModel) {
				m.Thinking = mistyped
				m.OutputConfig = invalidFormat
			},
			want: []string{"Error: Value Conversion Error @ "},
		},
		"invalid thinking type": {
			change: func(m *CountTokensDataSourceModel) {
				m.Thinking = invalidType
				m.OutputConfig = invalidFormat
			},
			want: []string{"Error: Invalid thinking type @ thinking.type"},
		},
		"output_config not decodable": {
			change: func(m *CountTokensDataSourceModel) {
				m.OutputConfig = mistyped
				m.CacheControl = mistyped
			},
			want: []string{"Error: Value Conversion Error @ "},
		},
		"format not an object": {
			change: func(m *CountTokensDataSourceModel) {
				m.OutputConfig = invalidFormat
				m.CacheControl = mistyped
			},
			want: []string{"Error: Invalid format @ output_config.format"},
		},
		"format null": {
			change: func(m *CountTokensDataSourceModel) {
				m.OutputConfig = ctOutputConfig(types.StringNull(), jsontypes.NewNormalizedValue(`null`))
			},
			want: []string{"Error: Invalid format @ output_config.format"},
		},
		"cache_control not decodable": {
			change: func(m *CountTokensDataSourceModel) { m.CacheControl = mistyped },
			want:   []string{"Error: Value Conversion Error @ "},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			m := ctModel(t)
			tc.change(&m)
			_, diags := buildCountTokensParams(ctx, m)
			if got := diagStrings(diags); !slices.Equal(got, tc.want) {
				t.Errorf("diagnostics = %q, want %q", got, tc.want)
			}
		})
	}
}
