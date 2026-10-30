// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package models

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/ippontech/terraform-provider-anthropic/internal/oauthtest"
)

func lifecycleStages(t *testing.T, stages ...string) types.Set {
	t.Helper()
	elems := make([]attr.Value, len(stages))
	for i, s := range stages {
		elems[i] = types.StringValue(s)
	}
	v, d := types.SetValue(types.StringType, elems)
	if d.HasError() {
		t.Fatalf("set: %v", d)
	}
	return v
}

func TestBuildModelsListParams(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		stages types.Set
		want   []string
	}{
		"null":    {stages: types.SetNull(types.StringType)},
		"unknown": {stages: types.SetUnknown(types.StringType)},
		"sorted":  {stages: lifecycleStages(t, "retired", "active"), want: []string{"active", "retired"}},
	} {
		t.Run(name, func(t *testing.T) {
			params, diags := buildModelsListParams(context.Background(), ModelsDataSourceModel{LifecycleStages: tc.stages})
			if diags.HasError() {
				t.Fatalf("diagnostics: %v", diags)
			}
			if !slices.Equal(params.Lifecycle, tc.want) {
				t.Errorf("lifecycle = %q, want %q", params.Lifecycle, tc.want)
			}
			if params.Limit.Value != 1000 {
				t.Errorf("limit = %v, want 1000", params.Limit)
			}
		})
	}
}

// Read sends lifecycle_stages as the API's lifecycle[] filter on every page,
// follows the after_id cursor and maps each model like anthropic_model.
func TestModelsDataSourceRead_lifecycleFilterAcrossPages(t *testing.T) {
	t.Parallel()
	var queries []url.Values
	srv := newModelsServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("path = %q, want /v1/models", r.URL.Path)
		}
		queries = append(queries, r.URL.Query())
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("after_id") == "" {
			_, _ = io.WriteString(w, `{"data":[`+activeModelJSON+`],"has_more":true,"first_id":"claude-opus-5-5","last_id":"claude-opus-5-5"}`)
			return
		}
		_, _ = io.WriteString(w, `{"data":[`+deprecatedModelJSON+`],"has_more":false,"first_id":"claude-sonnet-4-5-20250929","last_id":"claude-sonnet-4-5-20250929"}`)
	})

	stages := tftypes.NewValue(tftypes.Set{ElementType: tftypes.String}, []tftypes.Value{
		tftypes.NewValue(tftypes.String, "deprecated"),
		tftypes.NewValue(tftypes.String, "active"),
	})
	state := readDataSource(t, &ModelsDataSource{client: oauthtest.NewSDKClient(t, srv)}, map[string]tftypes.Value{
		"lifecycle_stages": stages,
	})

	if len(queries) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(queries))
	}
	for i, q := range queries {
		if got := q["lifecycle[]"]; !slices.Equal(got, []string{"active", "deprecated"}) {
			t.Errorf("request %d: lifecycle[] = %q, want [active deprecated] (query %v)", i, got, q)
		}
		if q.Get("limit") != "1000" {
			t.Errorf("request %d: limit = %q, want 1000", i, q.Get("limit"))
		}
	}
	if queries[1].Get("after_id") != "claude-opus-5-5" {
		t.Errorf("second request after_id = %q, want claude-opus-5-5", queries[1].Get("after_id"))
	}

	var models []ModelModel
	if d := state.GetAttribute(context.Background(), path.Root("models"), &models); d.HasError() {
		t.Fatalf("models: %v", d)
	}
	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}
	if models[0].LifecycleStage.ValueString() != "active" || !models[0].RetiresAt.IsNull() || !models[0].Line.IsNull() {
		t.Errorf("first model = %+v", models[0])
	}
	second := models[1]
	if second.ID.ValueString() != "claude-sonnet-4-5-20250929" || second.LifecycleStage.ValueString() != "deprecated" ||
		second.DeprecatedAt.ValueString() != "2026-08-01T00:00:00Z" || second.RetiresAt.ValueString() != "2027-02-01T00:00:00Z" ||
		second.Line.ValueString() != "sonnet" {
		t.Errorf("second model = %+v", second)
	}
	thinkingTypes, ok := second.Capabilities.Attributes()["thinking_types"].(types.Object)
	if !ok || !thinkingTypes.Attributes()["between_tools"].Equal(types.BoolValue(true)) {
		t.Errorf("second model thinking_types = %v", second.Capabilities.Attributes()["thinking_types"])
	}
}

// Without lifecycle_stages no lifecycle[] parameter is sent, so the API's own
// default applies (active and deprecated, never retired).
func TestModelsDataSourceRead_noFilterByDefault(t *testing.T) {
	t.Parallel()
	var query url.Values
	srv := newModelsServer(t, func(w http.ResponseWriter, r *http.Request) {
		query = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"data":[`+activeModelJSON+`],"has_more":false}`)
	})

	state := readDataSource(t, &ModelsDataSource{client: oauthtest.NewSDKClient(t, srv)}, nil)

	if query.Has("lifecycle[]") || query.Has("lifecycle") {
		t.Errorf("expected no lifecycle filter, got query %v", query)
	}
	var stages types.Set
	if d := state.GetAttribute(context.Background(), path.Root("lifecycle_stages"), &stages); d.HasError() || !stages.IsNull() {
		t.Errorf("lifecycle_stages = %v (%v), want null", stages, d)
	}
}

func TestModelsSchema_lifecycleStagesValidators(t *testing.T) {
	t.Parallel()
	var resp datasource.SchemaResponse
	(&ModelsDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &resp)
	attribute, ok := resp.Schema.Attributes["lifecycle_stages"].(schema.SetAttribute)
	if !ok {
		t.Fatal("lifecycle_stages is not a set attribute")
	}

	for name, tc := range map[string]struct {
		value   types.Set
		wantErr bool
	}{
		"all stages":    {value: lifecycleStages(t, "active", "deprecated", "retired")},
		"retired only":  {value: lifecycleStages(t, "retired")},
		"empty":         {value: lifecycleStages(t), wantErr: true},
		"unknown stage": {value: lifecycleStages(t, "active", "archived"), wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			req := validator.SetRequest{Path: path.Root("lifecycle_stages"), ConfigValue: tc.value}
			var diagsErr bool
			for _, v := range attribute.Validators {
				resp := &validator.SetResponse{}
				v.ValidateSet(context.Background(), req, resp)
				diagsErr = diagsErr || resp.Diagnostics.HasError()
			}
			if diagsErr != tc.wantErr {
				t.Errorf("error = %v, want %v", diagsErr, tc.wantErr)
			}
		})
	}
}
