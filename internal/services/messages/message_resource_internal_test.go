// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package messages

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
)

var cacheControlObjType = types.ObjectType{AttrTypes: map[string]attr.Type{"ttl": types.StringType}}

var messageObjType = types.ObjectType{AttrTypes: map[string]attr.Type{
	"role":          types.StringType,
	"content":       types.StringType,
	"cache_control": cacheControlObjType,
}}

func newTestModel(t *testing.T, msgs ...MessageResourceParamModel) *MessageResourceModel {
	t.Helper()
	if len(msgs) == 0 {
		msgs = []MessageResourceParamModel{{Role: types.StringValue("user"), Content: types.StringValue("hi")}}
	}
	list, diags := types.ListValueFrom(context.Background(), messageObjType, msgs)
	if diags.HasError() {
		t.Fatalf("building messages: %v", diags)
	}
	return &MessageResourceModel{
		Model:         types.StringValue("claude-haiku-4-5-20251001"),
		MaxTokens:     types.Int64Value(2048),
		Messages:      list,
		System:        types.StringNull(),
		Temperature:   types.Float64Null(),
		StopSequences: types.ListNull(types.StringType),
		ServiceTier:   types.StringNull(),
		InferenceGeo:  types.StringNull(),
	}
}

func marshalParams(t *testing.T, data *MessageResourceModel) map[string]any {
	t.Helper()
	params, diags := buildMessageParams(context.Background(), data)
	if diags.HasError() {
		t.Fatalf("buildMessageParams: %v", diags)
	}
	raw, err := params.MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}
	return out
}

func obj(t *testing.T, m map[string]any, key string) map[string]any {
	t.Helper()
	v, ok := m[key].(map[string]any)
	if !ok {
		t.Fatalf("expected object at %q, got %#v (full: %#v)", key, m[key], m)
	}
	return v
}

func TestBuildMessageParams_Minimal(t *testing.T) {
	out := marshalParams(t, newTestModel(t))
	for _, k := range []string{"thinking", "output_config", "cache_control", "metadata", "stop_sequences", "service_tier", "inference_geo", "system", "temperature"} {
		if _, ok := out[k]; ok {
			t.Errorf("unexpected key %q in minimal params: %#v", k, out)
		}
	}
}

// temperature is deprecated: an unset value must never reach the API (not even
// as 0), while a value the configuration still sets is sent as is, 0 included.
func TestBuildMessageParams_Temperature(t *testing.T) {
	tests := []struct {
		name     string
		value    types.Float64
		wantSent bool
		want     float64
	}{
		{name: "null is omitted", value: types.Float64Null()},
		{name: "unknown is omitted", value: types.Float64Unknown()},
		{name: "zero is sent", value: types.Float64Value(0), wantSent: true, want: 0},
		{name: "non-default is sent", value: types.Float64Value(0.5), wantSent: true, want: 0.5},
		{name: "default is sent", value: types.Float64Value(1), wantSent: true, want: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := newTestModel(t)
			data.Temperature = tc.value
			got, sent := marshalParams(t, data)["temperature"]
			if sent != tc.wantSent {
				t.Fatalf("temperature sent = %v, want %v (value %#v)", sent, tc.wantSent, got)
			}
			if sent && got != tc.want {
				t.Errorf("temperature = %#v, want %v", got, tc.want)
			}
		})
	}
}

// The deprecation warns without rejecting: under API version 2023-06-01 models
// older than Claude 4.7 still accept values other than 1.0, so the attribute
// keeps its 0.0-1.0 range and gains no "only 1.0" validator.
func TestMessageSchema_TemperatureDeprecated(t *testing.T) {
	var sresp resource.SchemaResponse
	NewMessageResource().Schema(context.Background(), resource.SchemaRequest{}, &sresp)
	temperature, ok := sresp.Schema.Attributes["temperature"].(schema.Float64Attribute)
	if !ok {
		t.Fatalf("temperature is %T, want schema.Float64Attribute", sresp.Schema.Attributes["temperature"])
	}
	for _, want := range []string{"next major version", "2026-10-21", "1.0 included"} {
		if !strings.Contains(temperature.DeprecationMessage, want) {
			t.Errorf("DeprecationMessage %q does not mention %q", temperature.DeprecationMessage, want)
		}
	}
	for _, v := range []float64{0, 0.5, 1} {
		for _, val := range temperature.Validators {
			req := validator.Float64Request{Path: path.Root("temperature"), ConfigValue: types.Float64Value(v)}
			var resp validator.Float64Response
			val.ValidateFloat64(context.Background(), req, &resp)
			if resp.Diagnostics.HasError() {
				t.Errorf("temperature = %v rejected: %v", v, resp.Diagnostics)
			}
		}
	}
}

func TestBuildMessageParams_SimpleFields(t *testing.T) {
	data := newTestModel(t)
	seqs, _ := types.ListValueFrom(context.Background(), types.StringType, []string{"END", "STOP"})
	data.StopSequences = seqs
	data.Metadata = &MetadataModel{UserID: types.StringValue("user-123")}
	data.ServiceTier = types.StringValue("standard_only")
	data.InferenceGeo = types.StringValue("us")

	out := marshalParams(t, data)
	if got := out["stop_sequences"]; len(got.([]any)) != 2 || got.([]any)[0] != "END" {
		t.Errorf("stop_sequences = %#v", got)
	}
	if got := obj(t, out, "metadata")["user_id"]; got != "user-123" {
		t.Errorf("metadata.user_id = %#v", got)
	}
	if out["service_tier"] != "standard_only" {
		t.Errorf("service_tier = %#v", out["service_tier"])
	}
	if out["inference_geo"] != "us" {
		t.Errorf("inference_geo = %#v", out["inference_geo"])
	}
}

func TestBuildMessageParams_Thinking(t *testing.T) {
	tests := []struct {
		name    string
		model   ThinkingModel
		want    map[string]any
		wantNot []string
	}{
		{
			name: "enabled",
			model: ThinkingModel{
				Type:         types.StringValue("enabled"),
				BudgetTokens: types.Int64Value(1024),
				Display:      types.StringValue("summarized"),
			},
			want: map[string]any{"type": "enabled", "budget_tokens": float64(1024), "display": "summarized"},
		},
		{
			name: "adaptive",
			model: ThinkingModel{
				Type:         types.StringValue("adaptive"),
				BudgetTokens: types.Int64Null(),
				Display:      types.StringValue("omitted"),
			},
			want:    map[string]any{"type": "adaptive", "display": "omitted"},
			wantNot: []string{"budget_tokens"},
		},
		{
			name:    "adaptive without display",
			model:   ThinkingModel{Type: types.StringValue("adaptive"), BudgetTokens: types.Int64Null(), Display: types.StringNull()},
			want:    map[string]any{"type": "adaptive"},
			wantNot: []string{"budget_tokens", "display"},
		},
		{
			name:    "disabled",
			model:   ThinkingModel{Type: types.StringValue("disabled"), BudgetTokens: types.Int64Null(), Display: types.StringNull()},
			want:    map[string]any{"type": "disabled"},
			wantNot: []string{"budget_tokens", "display"},
		},
		{
			name:    "between_tools",
			model:   ThinkingModel{Type: types.StringValue("between_tools"), BudgetTokens: types.Int64Null(), Display: types.StringNull()},
			want:    map[string]any{"type": "between_tools"},
			wantNot: []string{"budget_tokens", "display"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := newTestModel(t)
			m := tc.model
			data.Thinking = &m
			got := obj(t, marshalParams(t, data), "thinking")
			for k, v := range tc.want {
				if got[k] != v {
					t.Errorf("thinking[%q] = %#v, want %#v (full %#v)", k, got[k], v, got)
				}
			}
			for _, k := range tc.wantNot {
				if _, ok := got[k]; ok {
					t.Errorf("thinking must not contain %q: %#v", k, got)
				}
			}
		})
	}
}

func TestBuildMessageParams_OutputConfig(t *testing.T) {
	data := newTestModel(t)
	data.OutputConfig = &OutputConfigModel{
		Effort: types.StringValue("high"),
		Format: jsontypes.NewNormalizedValue(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"],"additionalProperties":false}`),
	}
	oc := obj(t, marshalParams(t, data), "output_config")
	if oc["effort"] != "high" {
		t.Errorf("effort = %#v", oc["effort"])
	}
	format := obj(t, oc, "format")
	if format["type"] != "json_schema" {
		t.Errorf("format.type = %#v", format["type"])
	}
	schema := obj(t, format, "schema")
	if schema["type"] != "object" {
		t.Errorf("format.schema.type = %#v", schema["type"])
	}

	// effort only: no format key
	data.OutputConfig = &OutputConfigModel{Effort: types.StringValue("low"), Format: jsontypes.NewNormalizedNull()}
	oc = obj(t, marshalParams(t, data), "output_config")
	if _, ok := oc["format"]; ok {
		t.Errorf("format must be omitted when null: %#v", oc)
	}
}

func TestBuildMessageParams_OutputConfigInvalidFormat(t *testing.T) {
	data := newTestModel(t)
	data.OutputConfig = &OutputConfigModel{Effort: types.StringNull(), Format: jsontypes.NewNormalizedValue(`["not","an","object"]`)}
	_, diags := buildMessageParams(context.Background(), data)
	if !diags.HasError() {
		t.Fatal("expected an error for a non-object JSON schema")
	}
}

func TestBuildMessageParams_CacheControlAllLevels(t *testing.T) {
	data := newTestModel(t,
		MessageResourceParamModel{Role: types.StringValue("user"), Content: types.StringValue("a"), CacheControl: &CacheControlModel{TTL: types.StringValue("1h")}},
		MessageResourceParamModel{Role: types.StringValue("assistant"), Content: types.StringValue("b")},
		MessageResourceParamModel{Role: types.StringValue("user"), Content: types.StringValue("c"), CacheControl: &CacheControlModel{TTL: types.StringNull()}},
	)
	data.System = types.StringValue("sys")
	data.CacheControl = &CacheControlModel{TTL: types.StringNull()}

	out := marshalParams(t, data)

	top := obj(t, out, "cache_control")
	if top["type"] != "ephemeral" {
		t.Errorf("top-level cache_control = %#v", top)
	}
	if _, ok := top["ttl"]; ok {
		t.Errorf("top-level ttl must be omitted when null: %#v", top)
	}

	sys := out["system"].([]any)[0].(map[string]any)
	if _, ok := sys["cache_control"]; ok {
		t.Errorf("system block must not carry cache_control: %#v", sys)
	}

	msgs := out["messages"].([]any)
	block := func(i int) map[string]any {
		return msgs[i].(map[string]any)["content"].([]any)[0].(map[string]any)
	}
	if cc := obj(t, block(0), "cache_control"); cc["ttl"] != "1h" || cc["type"] != "ephemeral" {
		t.Errorf("messages[0] cache_control = %#v", cc)
	}
	if _, ok := block(1)["cache_control"]; ok {
		t.Errorf("messages[1] must not carry cache_control: %#v", block(1))
	}
	if cc := obj(t, block(2), "cache_control"); cc["type"] != "ephemeral" {
		t.Errorf("messages[2] cache_control = %#v", cc)
	}
}

func decodeMessage(t *testing.T, raw string) *anthropic.Message {
	t.Helper()
	var m anthropic.Message
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("unmarshal message: %v", err)
	}
	return &m
}

func TestMapMessageToState_Full(t *testing.T) {
	msg := decodeMessage(t, `{
	  "id": "msg_1", "type": "message", "role": "assistant", "model": "m",
	  "content": [{"type":"thinking","thinking":"hm","signature":"s"},{"type":"text","text":"Hello "},{"type":"text","text":"world"}],
	  "stop_reason": "refusal",
	  "stop_sequence": null,
	  "stop_details": {"type":"refusal","category":"cyber","explanation":"nope"},
	  "usage": {"input_tokens": 10, "output_tokens": 50, "cache_creation_input_tokens": 3, "cache_read_input_tokens": 4,
	            "output_tokens_details": {"thinking_tokens": 30}, "service_tier": "standard", "inference_geo": "us"}
	}`)
	var data MessageResourceModel
	mapMessageToState(msg, &data)

	if data.Content.ValueString() != "Hello world" {
		t.Errorf("content = %q", data.Content.ValueString())
	}
	if !data.StopSequence.IsNull() {
		t.Errorf("stop_sequence should be null, got %v", data.StopSequence)
	}
	attrs := data.StopDetails.Attributes()
	if data.StopDetails.IsNull() || attrs["category"] != types.StringValue("cyber") || attrs["explanation"] != types.StringValue("nope") {
		t.Errorf("stop_details = %v", data.StopDetails)
	}
	if data.ThinkingTokens.ValueInt64() != 30 || data.CacheCreationInputTokens.ValueInt64() != 3 || data.CacheReadInputTokens.ValueInt64() != 4 {
		t.Errorf("token counts mismatch: %v %v %v", data.ThinkingTokens, data.CacheCreationInputTokens, data.CacheReadInputTokens)
	}
	if data.UsageServiceTier.ValueString() != "standard" || data.UsageInferenceGeo.ValueString() != "us" {
		t.Errorf("usage tier/geo = %v / %v", data.UsageServiceTier, data.UsageInferenceGeo)
	}
}

func TestMapMessageToState_NoStopDetailsNoOptionals(t *testing.T) {
	msg := decodeMessage(t, `{
	  "id": "msg_2", "type": "message", "role": "assistant", "model": "m",
	  "content": [{"type":"text","text":"hi"}],
	  "stop_reason": "stop_sequence", "stop_sequence": "END", "stop_details": null,
	  "usage": {"input_tokens": 1, "output_tokens": 2, "cache_creation_input_tokens": 0, "cache_read_input_tokens": 0,
	            "output_tokens_details": {"thinking_tokens": 0}, "service_tier": "", "inference_geo": ""}
	}`)
	var data MessageResourceModel
	data.StopDetails = types.ObjectValueMust(stopDetailsAttrTypes, map[string]attr.Value{
		"category": types.StringValue("x"), "explanation": types.StringValue("y"),
	}) // must be reset
	mapMessageToState(msg, &data)

	if !data.StopDetails.IsNull() {
		t.Errorf("stop_details should be null, got %v", data.StopDetails)
	}
	if data.StopSequence.ValueString() != "END" {
		t.Errorf("stop_sequence = %v", data.StopSequence)
	}
	if !data.UsageServiceTier.IsNull() || !data.UsageInferenceGeo.IsNull() {
		t.Errorf("empty tier/geo must map to null: %v / %v", data.UsageServiceTier, data.UsageInferenceGeo)
	}
	if data.ThinkingTokens.IsNull() || data.ThinkingTokens.ValueInt64() != 0 {
		t.Errorf("thinking_tokens = %v", data.ThinkingTokens)
	}
}

// validateConfig runs the config validator against a raw config value.
func validateConfig(t *testing.T, cfg map[string]tftypes.Value) diag.Diagnostics {
	t.Helper()
	r := NewMessageResource()
	var sresp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &sresp)
	typ := sresp.Schema.Type().TerraformType(context.Background()).(tftypes.Object)

	vals := map[string]tftypes.Value{}
	for name, at := range typ.AttributeTypes {
		vals[name] = tftypes.NewValue(at, nil)
	}
	for k, v := range cfg {
		vals[k] = v
	}
	req := resource.ValidateConfigRequest{Config: tfsdk.Config{Schema: sresp.Schema, Raw: tftypes.NewValue(typ, vals)}}
	var resp resource.ValidateConfigResponse
	for _, v := range r.(resource.ResourceWithConfigValidators).ConfigValidators(context.Background()) {
		v.ValidateResource(context.Background(), req, &resp)
	}
	return resp.Diagnostics
}

func thinkingValue(t *testing.T, typ, budget, display any) map[string]tftypes.Value {
	t.Helper()
	r := NewMessageResource()
	var sresp resource.SchemaResponse
	r.Schema(context.Background(), resource.SchemaRequest{}, &sresp)
	ot := sresp.Schema.Type().TerraformType(context.Background()).(tftypes.Object).AttributeTypes["thinking"].(tftypes.Object)
	return map[string]tftypes.Value{"thinking": tftypes.NewValue(ot, map[string]tftypes.Value{
		"type":          tftypes.NewValue(tftypes.String, typ),
		"budget_tokens": tftypes.NewValue(tftypes.Number, budget),
		"display":       tftypes.NewValue(tftypes.String, display),
	})}
}

func TestConfigValidator_Thinking(t *testing.T) {
	unknown := tftypes.UnknownValue
	tests := []struct {
		name    string
		typ     any
		budget  any
		display any
		wantErr bool
	}{
		{"enabled with budget", "enabled", 1024, nil, false},
		{"enabled without budget", "enabled", nil, nil, true},
		{"enabled with unknown budget", "enabled", unknown, nil, false},
		{"adaptive without budget", "adaptive", nil, "summarized", false},
		{"adaptive with budget", "adaptive", 2048, nil, true},
		{"disabled plain", "disabled", nil, nil, false},
		{"disabled with display", "disabled", nil, "omitted", true},
		{"disabled with budget", "disabled", 1024, nil, true},
		{"between_tools plain", "between_tools", nil, nil, false},
		{"between_tools with display", "between_tools", nil, "summarized", true},
		{"between_tools with budget", "between_tools", 1024, nil, true},
		{"unknown type", unknown, nil, nil, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			diags := validateConfig(t, thinkingValue(t, tc.typ, tc.budget, tc.display))
			if diags.HasError() != tc.wantErr {
				t.Errorf("wantErr=%v, diags=%v", tc.wantErr, diags)
			}
		})
	}
}

// speed and diagnostics are omitted from the request unless configured.
func TestBuildMessageParams_SpeedAndDiagnosticsOmittedWhenUnset(t *testing.T) {
	out := marshalParams(t, newTestModel(t))
	for _, k := range []string{"speed", "diagnostics"} {
		if _, ok := out[k]; ok {
			t.Errorf("unexpected key %q in minimal params: %#v", k, out)
		}
	}
}

func TestBuildMessageParams_Speed(t *testing.T) {
	for _, speed := range []string{"standard", "fast"} {
		data := newTestModel(t)
		data.Speed = types.StringValue(speed)
		if got := marshalParams(t, data)["speed"]; got != speed {
			t.Errorf("speed = %#v, want %q", got, speed)
		}
	}
}

// Without previous_message_id the request carries an explicit
// "previous_message_id": null, the documented first-turn opt-in; with it, the
// ID. cache_miss_reason (Computed) never reaches the request.
func TestBuildMessageParams_Diagnostics(t *testing.T) {
	data := newTestModel(t)
	data.Diagnostics = &DiagnosticsModel{
		PreviousMessageID: types.StringNull(),
		CacheMissReason:   types.ObjectUnknown(cacheMissReasonAttrTypes),
	}
	first := obj(t, marshalParams(t, data), "diagnostics")
	if v, ok := first["previous_message_id"]; !ok || v != nil {
		t.Errorf("first turn diagnostics = %#v, want {\"previous_message_id\": null}", first)
	}
	if len(first) != 1 {
		t.Errorf("diagnostics must only carry previous_message_id: %#v", first)
	}

	data.Diagnostics.PreviousMessageID = types.StringValue("msg_01ABC")
	next := obj(t, marshalParams(t, data), "diagnostics")
	if next["previous_message_id"] != "msg_01ABC" || len(next) != 1 {
		t.Errorf("next turn diagnostics = %#v", next)
	}
}

func TestMapMessageToState_SpeedAndCacheMissReason(t *testing.T) {
	const usage = `"usage": {"input_tokens": 1, "output_tokens": 2, "speed": "fast"}`
	for _, tc := range []struct {
		name        string
		diagnostics string
		wantType    string // "" means a null cache_miss_reason
		wantMissed  *int64
	}{
		{name: "changed", diagnostics: `{"cache_miss_reason": {"type": "system_changed", "cache_missed_input_tokens": 41850}}`,
			wantType: "system_changed", wantMissed: func() *int64 { v := int64(41850); return &v }()},
		{name: "no token estimate", diagnostics: `{"cache_miss_reason": {"type": "previous_message_not_found"}}`,
			wantType: "previous_message_not_found"},
		{name: "pending", diagnostics: `{"cache_miss_reason": null}`},
		{name: "no divergence", diagnostics: `null`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msg := decodeMessage(t, `{"id": "msg_3", "type": "message", "role": "assistant", "model": "m",
			  "content": [{"type":"text","text":"hi"}], "stop_reason": "end_turn",
			  "diagnostics": `+tc.diagnostics+`, `+usage+`}`)
			data := MessageResourceModel{Diagnostics: &DiagnosticsModel{
				PreviousMessageID: types.StringValue("msg_2"),
				CacheMissReason:   types.ObjectUnknown(cacheMissReasonAttrTypes),
			}}
			mapMessageToState(msg, &data)

			if data.UsageSpeed.ValueString() != "fast" {
				t.Errorf("usage_speed = %v", data.UsageSpeed)
			}
			if data.Diagnostics.PreviousMessageID.ValueString() != "msg_2" {
				t.Errorf("previous_message_id must keep its planned value, got %v", data.Diagnostics.PreviousMessageID)
			}
			reason := data.Diagnostics.CacheMissReason
			if tc.wantType == "" {
				if !reason.IsNull() {
					t.Errorf("cache_miss_reason = %v, want null", reason)
				}
				return
			}
			attrs := reason.Attributes()
			if attrs["type"] != types.StringValue(tc.wantType) {
				t.Errorf("type = %v, want %s", attrs["type"], tc.wantType)
			}
			wantMissed := types.Int64Null()
			if tc.wantMissed != nil {
				wantMissed = types.Int64Value(*tc.wantMissed)
			}
			if !attrs["cache_missed_input_tokens"].Equal(wantMissed) {
				t.Errorf("cache_missed_input_tokens = %v, want %v", attrs["cache_missed_input_tokens"], wantMissed)
			}
		})
	}
}

// Without a diagnostics block nothing is mapped (the block stays null), and a
// response without usage.speed leaves usage_speed null.
func TestMapMessageToState_NoDiagnosticsNoSpeed(t *testing.T) {
	msg := decodeMessage(t, `{"id": "msg_4", "type": "message", "role": "assistant", "model": "m",
	  "content": [], "stop_reason": "end_turn", "diagnostics": null, "usage": {"input_tokens": 1, "output_tokens": 2}}`)
	var data MessageResourceModel
	mapMessageToState(msg, &data)
	if data.Diagnostics != nil {
		t.Errorf("diagnostics = %+v, want nil", data.Diagnostics)
	}
	if !data.UsageSpeed.IsNull() {
		t.Errorf("usage_speed = %v, want null", data.UsageSpeed)
	}
}

// Like every other input, speed and diagnostics replace the message when they
// change; diagnostics.cache_miss_reason is the response side of the block.
func TestMessageSchema_SpeedAndDiagnostics(t *testing.T) {
	var sresp resource.SchemaResponse
	NewMessageResource().Schema(context.Background(), resource.SchemaRequest{}, &sresp)
	if diags := sresp.Schema.ValidateImplementation(context.Background()); diags.HasError() {
		t.Fatalf("schema: %v", diags)
	}

	speed, ok := sresp.Schema.Attributes["speed"].(schema.StringAttribute)
	if !ok || !speed.Optional || speed.Computed || len(speed.PlanModifiers) != 1 ||
		speed.PlanModifiers[0].Description(context.Background()) != stringplanmodifier.RequiresReplace().Description(context.Background()) {
		t.Errorf("speed must be Optional with RequiresReplace: %#v", sresp.Schema.Attributes["speed"])
	}
	diagnostics, ok := sresp.Schema.Attributes["diagnostics"].(schema.SingleNestedAttribute)
	if !ok || !diagnostics.Optional || diagnostics.Computed || len(diagnostics.PlanModifiers) != 1 ||
		diagnostics.PlanModifiers[0].Description(context.Background()) != objectplanmodifier.RequiresReplace().Description(context.Background()) {
		t.Fatalf("diagnostics must be Optional with RequiresReplace: %#v", sresp.Schema.Attributes["diagnostics"])
	}
	if a := diagnostics.Attributes["previous_message_id"]; !a.IsOptional() || a.IsComputed() {
		t.Error("diagnostics.previous_message_id must be Optional")
	}
	if a := diagnostics.Attributes["cache_miss_reason"]; a.IsOptional() || !a.IsComputed() {
		t.Error("diagnostics.cache_miss_reason must be Computed only")
	}
	if !sresp.Schema.Attributes["usage_speed"].IsComputed() {
		t.Error("usage_speed must be Computed")
	}
}
