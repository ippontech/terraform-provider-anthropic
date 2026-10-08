// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package messages

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/ippontech/terraform-provider-anthropic/internal/oauthtest"
)

var (
	ctThinkingAttrTypes = map[string]attr.Type{
		"type": types.StringType, "budget_tokens": types.Int64Type, "display": types.StringType,
	}
	ctOutputConfigAttrTypes = map[string]attr.Type{
		"effort": types.StringType, "format": jsontypes.NormalizedType{},
	}
	ctCacheControlAttrTypes = map[string]attr.Type{"ttl": types.StringType}
	ctMessageAttrTypes      = map[string]attr.Type{"role": types.StringType, "content": types.StringType}
)

func ctModel(t *testing.T) CountTokensDataSourceModel {
	t.Helper()
	msgs, diags := types.ListValue(types.ObjectType{AttrTypes: ctMessageAttrTypes}, []attr.Value{
		types.ObjectValueMust(ctMessageAttrTypes, map[string]attr.Value{
			"role": types.StringValue("user"), "content": types.StringValue("Hello"),
		}),
	})
	if diags.HasError() {
		t.Fatal(diags)
	}
	return CountTokensDataSourceModel{
		Model:        types.StringValue("claude-haiku-4-5-20251001"),
		Messages:     msgs,
		System:       types.StringNull(),
		Thinking:     types.ObjectNull(ctThinkingAttrTypes),
		OutputConfig: types.ObjectNull(ctOutputConfigAttrTypes),
		CacheControl: types.ObjectNull(ctCacheControlAttrTypes),
		InputTokens:  types.Int64Null(),
	}
}

func ctThinking(typ string, budget *int64, display string) types.Object {
	b := types.Int64Null()
	if budget != nil {
		b = types.Int64Value(*budget)
	}
	d := types.StringNull()
	if display != "" {
		d = types.StringValue(display)
	}
	return types.ObjectValueMust(ctThinkingAttrTypes, map[string]attr.Value{
		"type": types.StringValue(typ), "budget_tokens": b, "display": d,
	})
}

func ctMarshal(t *testing.T, m CountTokensDataSourceModel) map[string]any {
	t.Helper()
	params, diags := buildCountTokensParams(context.Background(), m)
	if diags.HasError() {
		t.Fatalf("unexpected diagnostics: %v", diags)
	}
	raw, err := params.MarshalJSON()
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestBuildCountTokensParams_Thinking(t *testing.T) {
	budget := int64(2048)
	cases := []struct {
		name     string
		thinking types.Object
		want     map[string]any
	}{
		{"enabled", ctThinking("enabled", &budget, ""), map[string]any{"type": "enabled", "budget_tokens": float64(2048)}},
		{"enabled with display", ctThinking("enabled", &budget, "omitted"), map[string]any{"type": "enabled", "budget_tokens": float64(2048), "display": "omitted"}},
		{"disabled", ctThinking("disabled", nil, ""), map[string]any{"type": "disabled"}},
		{"adaptive", ctThinking("adaptive", nil, ""), map[string]any{"type": "adaptive"}},
		{"adaptive with display", ctThinking("adaptive", nil, "summarized"), map[string]any{"type": "adaptive", "display": "summarized"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := ctModel(t)
			m.Thinking = tc.thinking
			got := ctMarshal(t, m)["thinking"]
			gotJSON, _ := json.Marshal(got)
			wantJSON, _ := json.Marshal(tc.want)
			if string(gotJSON) != string(wantJSON) {
				t.Fatalf("thinking = %s, want %s", gotJSON, wantJSON)
			}
		})
	}
}

func TestBuildCountTokensParams_OutputConfigAndCacheControl(t *testing.T) {
	m := ctModel(t)
	m.OutputConfig = types.ObjectValueMust(ctOutputConfigAttrTypes, map[string]attr.Value{
		"effort": types.StringValue("xhigh"),
		"format": jsontypes.NewNormalizedValue(`{"type":"object","properties":{"a":{"type":"string"}}}`),
	})
	m.CacheControl = types.ObjectValueMust(ctCacheControlAttrTypes, map[string]attr.Value{"ttl": types.StringValue("1h")})

	got := ctMarshal(t, m)

	oc, _ := json.Marshal(got["output_config"])
	wantOC := `{"effort":"xhigh","format":{"schema":{"properties":{"a":{"type":"string"}},"type":"object"},"type":"json_schema"}}`
	if string(oc) != wantOC {
		t.Errorf("output_config = %s, want %s", oc, wantOC)
	}
	cc, _ := json.Marshal(got["cache_control"])
	if string(cc) != `{"ttl":"1h","type":"ephemeral"}` {
		t.Errorf("cache_control = %s", cc)
	}
}

func TestBuildCountTokensParams_OmitsUnsetBlocks(t *testing.T) {
	got := ctMarshal(t, ctModel(t))
	for _, k := range []string{"thinking", "output_config", "cache_control"} {
		if _, ok := got[k]; ok {
			t.Errorf("%s must be omitted when unset, got %v", k, got[k])
		}
	}
}

func TestBuildCountTokensParams_InvalidFormat(t *testing.T) {
	m := ctModel(t)
	m.OutputConfig = types.ObjectValueMust(ctOutputConfigAttrTypes, map[string]attr.Value{
		"effort": types.StringNull(), "format": jsontypes.NewNormalizedValue(`[1]`),
	})
	if _, diags := buildCountTokensParams(context.Background(), m); !diags.HasError() {
		t.Fatal("expected an error for a non-object format")
	}
}

func TestCountTokensThinkingValidator(t *testing.T) {
	ds := &CountTokensDataSource{}
	var schemaResp datasource.SchemaResponse
	ds.Schema(context.Background(), datasource.SchemaRequest{}, &schemaResp)
	schemaType := schemaResp.Schema.Type().TerraformType(context.Background())

	budget := int64(1024)
	cases := []struct {
		name    string
		obj     types.Object
		wantErr bool
	}{
		{"enabled with budget", ctThinking("enabled", &budget, ""), false},
		{"enabled without budget", ctThinking("enabled", nil, ""), true},
		{"enabled unknown budget", types.ObjectValueMust(ctThinkingAttrTypes, map[string]attr.Value{
			"type": types.StringValue("enabled"), "budget_tokens": types.Int64Unknown(), "display": types.StringNull(),
		}), false},
		{"disabled", ctThinking("disabled", nil, ""), false},
		{"disabled with display", ctThinking("disabled", nil, "omitted"), true},
		{"disabled with budget", ctThinking("disabled", &budget, ""), true},
		{"adaptive with display", ctThinking("adaptive", nil, "omitted"), false},
		{"adaptive with budget", ctThinking("adaptive", &budget, ""), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := ctModel(t)
			m.Thinking = tc.obj
			st := tfsdk.State{Schema: schemaResp.Schema, Raw: tftypes.NewValue(schemaType, nil)}
			if d := st.Set(context.Background(), &m); d.HasError() {
				t.Fatal(d)
			}
			cfg := tfsdk.Config{Schema: schemaResp.Schema, Raw: st.Raw}
			var resp datasource.ValidateConfigResponse
			countTokensThinkingValidator{}.ValidateDataSource(context.Background(), datasource.ValidateConfigRequest{Config: cfg}, &resp)
			if resp.Diagnostics.HasError() != tc.wantErr {
				t.Fatalf("HasError = %v, want %v (%v)", resp.Diagnostics.HasError(), tc.wantErr, resp.Diagnostics)
			}
		})
	}
}

func TestCountTokensDataSource_RoundTrip(t *testing.T) {
	var gotBody map[string]any
	var gotBeta string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages/count_tokens" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		gotBeta = r.Header.Get("anthropic-beta")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"input_tokens":42}`))
	}))
	defer srv.Close()

	budget := int64(1024)
	m := ctModel(t)
	m.Thinking = ctThinking("enabled", &budget, "")
	params, diags := buildCountTokensParams(context.Background(), m)
	if diags.HasError() {
		t.Fatal(diags)
	}

	client := oauthtest.NewSDKClient(t, srv)
	res, err := client.Messages.CountTokens(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if res.InputTokens != 42 {
		t.Errorf("input_tokens = %d, want 42", res.InputTokens)
	}
	th, _ := gotBody["thinking"].(map[string]any)
	if th["type"] != "enabled" || th["budget_tokens"] != float64(1024) {
		t.Errorf("request thinking = %v", gotBody["thinking"])
	}
	if gotBeta != "" {
		t.Errorf("no beta header expected, got %q", gotBeta)
	}
}
