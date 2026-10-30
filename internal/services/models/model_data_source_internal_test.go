// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package models

import (
	"context"
	"io"
	"maps"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/ippontech/terraform-provider-anthropic/internal/oauthtest"
)

// deprecatedModelJSON is a deprecated model in the shape of the Models API
// reference (https://platform.claude.com/docs/en/api/models/list), with every
// capability flag chosen so that each one maps from its own key.
const deprecatedModelJSON = `{
  "id": "claude-sonnet-4-5-20250929",
  "type": "model",
  "display_name": "Claude Sonnet 4.5",
  "created_at": "2025-09-29T00:00:00Z",
  "lifecycle": "deprecated",
  "deprecated_at": "2026-08-01T00:00:00Z",
  "retires_at": "2027-02-01T00:00:00Z",
  "line": "sonnet",
  "max_input_tokens": 200000,
  "max_tokens": 64000,
  "capabilities": {
    "batch": {"supported": true},
    "citations": {"supported": true},
    "code_execution": {"supported": false},
    "context_management": {
      "clear_thinking_20251015": {"supported": true},
      "clear_tool_uses_20250919": {"supported": true},
      "compact_20260112": {"supported": false},
      "supported": true
    },
    "effort": {
      "high": {"supported": true}, "low": {"supported": true}, "max": {"supported": false},
      "medium": {"supported": true}, "xhigh": {"supported": false}, "supported": true
    },
    "image_input": {"supported": true},
    "pdf_input": {"supported": true},
    "server_tools": {"code_execution": {"supported": true}, "web_search": {"supported": false}, "supported": true},
    "structured_outputs": {"supported": true},
    "thinking": {
      "supported": true,
      "types": {
        "adaptive": {"supported": false},
        "between_tools": {"supported": true},
        "disabled": {"supported": false},
        "enabled": {"supported": true}
      }
    }
  }
}`

// activeModelJSON is an active model: deprecated_at, retires_at and line are
// null, as the reference documents them for a model in no line that is not
// scheduled for retirement.
const activeModelJSON = `{
  "id": "claude-opus-5-5",
  "type": "model",
  "display_name": "Claude Opus 5.5",
  "created_at": "2026-09-21T00:00:00Z",
  "lifecycle": "active",
  "deprecated_at": null,
  "retires_at": null,
  "line": null,
  "max_input_tokens": 1000000,
  "max_tokens": 128000,
  "capabilities": {
    "batch": {"supported": true},
    "citations": {"supported": true},
    "code_execution": {"supported": true},
    "context_management": {"supported": false},
    "effort": {"supported": false},
    "image_input": {"supported": true},
    "pdf_input": {"supported": true},
    "server_tools": {"code_execution": {"supported": true}, "web_search": {"supported": true}, "supported": true},
    "structured_outputs": {"supported": true},
    "thinking": {"supported": false, "types": {"disabled": {"supported": true}}}
  }
}`

// readDataSource runs d.Read with a config built from vals (every other
// attribute null) and returns the resulting state.
func readDataSource(t *testing.T, d datasource.DataSource, vals map[string]tftypes.Value) tfsdk.State {
	t.Helper()
	ctx := context.Background()
	var schemaResp datasource.SchemaResponse
	d.Schema(ctx, datasource.SchemaRequest{}, &schemaResp)
	if schemaResp.Diagnostics.HasError() {
		t.Fatalf("schema: %v", schemaResp.Diagnostics)
	}
	typ := schemaResp.Schema.Type().TerraformType(ctx).(tftypes.Object)
	all := make(map[string]tftypes.Value, len(typ.AttributeTypes))
	for name, attrType := range typ.AttributeTypes {
		all[name] = tftypes.NewValue(attrType, nil)
	}
	maps.Copy(all, vals)
	req := datasource.ReadRequest{Config: tfsdk.Config{Raw: tftypes.NewValue(typ, all), Schema: schemaResp.Schema}}
	resp := &datasource.ReadResponse{State: tfsdk.State{Schema: schemaResp.Schema}}
	d.Read(ctx, req, resp)
	if resp.Diagnostics.HasError() {
		t.Fatalf("read: %v", resp.Diagnostics)
	}
	return resp.State
}

// assertAttrs checks string and bool values at the given paths of state.
// A nil string pointer expects null.
func assertAttrs(t *testing.T, state tfsdk.State, strs map[string]*string, bools map[string]bool) {
	t.Helper()
	ctx := context.Background()
	for p, want := range strs {
		var got types.String
		if d := state.GetAttribute(ctx, attrPath(p), &got); d.HasError() {
			t.Fatalf("%s: %v", p, d)
		}
		switch {
		case want == nil && !got.IsNull():
			t.Errorf("%s = %q, want null", p, got.ValueString())
		case want != nil && got.ValueString() != *want:
			t.Errorf("%s = %q (null %v), want %q", p, got.ValueString(), got.IsNull(), *want)
		}
	}
	for p, want := range bools {
		var got types.Bool
		if d := state.GetAttribute(ctx, attrPath(p), &got); d.HasError() {
			t.Fatalf("%s: %v", p, d)
		}
		if got.IsNull() || got.ValueBool() != want {
			t.Errorf("%s = %v, want %v", p, got, want)
		}
	}
}

// attrPath turns "a.b.c" into path.Root("a").AtName("b").AtName("c").
func attrPath(dotted string) path.Path {
	parts := strings.Split(dotted, ".")
	p := path.Root(parts[0])
	for _, part := range parts[1:] {
		p = p.AtName(part)
	}
	return p
}

func newModelsServer(t *testing.T, handler http.HandlerFunc) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

func TestModelDataSourceRead_lifecycleAndCapabilities(t *testing.T) {
	t.Parallel()
	var gotPath string
	srv := newModelsServer(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, deprecatedModelJSON)
	})

	state := readDataSource(t, &ModelDataSource{client: oauthtest.NewSDKClient(t, srv)}, map[string]tftypes.Value{
		"model_id": tftypes.NewValue(tftypes.String, "claude-sonnet-4-5"),
	})

	if gotPath != "/v1/models/claude-sonnet-4-5" {
		t.Errorf("path = %q, want /v1/models/claude-sonnet-4-5", gotPath)
	}
	assertAttrs(t, state, map[string]*string{
		"model_id":        new("claude-sonnet-4-5"),
		"id":              new("claude-sonnet-4-5-20250929"),
		"display_name":    new("Claude Sonnet 4.5"),
		"created_at":      new("2025-09-29T00:00:00Z"),
		"lifecycle_stage": new("deprecated"),
		"deprecated_at":   new("2026-08-01T00:00:00Z"),
		"retires_at":      new("2027-02-01T00:00:00Z"),
		"line":            new("sonnet"),
	}, map[string]bool{
		"capabilities.batch":                                       true,
		"capabilities.citations":                                   true,
		"capabilities.code_execution":                              false,
		"capabilities.context_management.clear_thinking_20251015":  true,
		"capabilities.context_management.clear_tool_uses_20250919": true,
		"capabilities.context_management.compact_20260112":         false,
		"capabilities.effort.high":                                 true,
		"capabilities.effort.max":                                  false,
		"capabilities.server_tools.code_execution":                 true,
		"capabilities.server_tools.web_search":                     false,
		"capabilities.thinking":                                    true,
		"capabilities.thinking_types.adaptive":                     false,
		"capabilities.thinking_types.between_tools":                true,
		"capabilities.thinking_types.disabled":                     false,
		"capabilities.thinking_types.enabled":                      true,
	})

	var maxInput, maxTokens types.Int64
	d := state.GetAttribute(context.Background(), path.Root("max_input_tokens"), &maxInput)
	d.Append(state.GetAttribute(context.Background(), path.Root("max_tokens"), &maxTokens)...)
	if d.HasError() || maxInput.ValueInt64() != 200000 || maxTokens.ValueInt64() != 64000 {
		t.Errorf("max_input_tokens = %v, max_tokens = %v", maxInput, maxTokens)
	}
}

// An active model has no deprecation or retirement date, and a model in no
// line has a null line: all three read as null, never as "" or a zero time.
// The boolean capability thinking keeps its meaning next to thinking_types.
func TestModelDataSourceRead_activeModelNulls(t *testing.T) {
	t.Parallel()
	srv := newModelsServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, activeModelJSON)
	})

	state := readDataSource(t, &ModelDataSource{client: oauthtest.NewSDKClient(t, srv)}, map[string]tftypes.Value{
		"model_id": tftypes.NewValue(tftypes.String, "claude-opus-5-5"),
	})

	assertAttrs(t, state, map[string]*string{
		"lifecycle_stage": new("active"),
		"deprecated_at":   nil,
		"retires_at":      nil,
		"line":            nil,
	}, map[string]bool{
		"capabilities.code_execution":              true,
		"capabilities.server_tools.code_execution": true,
		"capabilities.server_tools.web_search":     true,
		"capabilities.thinking":                    false,
		"capabilities.thinking_types.disabled":     true,
		"capabilities.thinking_types.adaptive":     false,
		"capabilities.thinking_types.enabled":      false,
	})
}

// `lifecycle` is a reserved root attribute name (a Terraform meta-argument),
// which is why the attributes are lifecycle_stage / lifecycle_stages. The
// framework only checks this when a provider server builds its schemas, so
// pin it here.
func TestModelSchemas_validImplementation(t *testing.T) {
	t.Parallel()
	for name, d := range map[string]datasource.DataSource{
		"anthropic_model":  &ModelDataSource{},
		"anthropic_models": &ModelsDataSource{},
	} {
		var resp datasource.SchemaResponse
		d.Schema(context.Background(), datasource.SchemaRequest{}, &resp)
		if diags := resp.Schema.ValidateImplementation(context.Background()); diags.HasError() {
			t.Errorf("%s: %v", name, diags)
		}
	}
}

// capabilities.code_execution no longer means support for the code execution
// tool: that is server_tools.code_execution.
func TestModelSchema_codeExecutionDescription(t *testing.T) {
	t.Parallel()
	caps, ok := modelAttributes()["capabilities"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatal("capabilities is not a single nested attribute")
	}
	desc := caps.Attributes["code_execution"].GetMarkdownDescription()
	if !strings.Contains(desc, "call the request's other tools") || !strings.Contains(desc, "server_tools.code_execution") {
		t.Errorf("code_execution description = %q", desc)
	}
	if caps.Attributes["thinking"].GetDeprecationMessage() != "" {
		t.Error("capabilities.thinking still means thinking support and must not be deprecated")
	}
}
