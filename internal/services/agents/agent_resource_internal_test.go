// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/ippontech/terraform-provider-anthropic/internal/oauthtest"
	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
)

// TestMapAgentResponseToState_customToolInputSchema is a regression test for the
// anthropic-sdk-go upgrade (v1.37 -> v1.51) that changed how the
// BetaManagedAgentsCustomToolInputSchema struct marshals: field reordering plus a
// spurious "ExtraFields":null key. Re-marshaling the parsed schema with
// encoding/json produced a state value that no longer matched the user's planned
// input_schema string, triggering "Provider produced inconsistent result after
// apply". The mapping now preserves the raw API JSON via RawJSON() and stores it
// as a jsontypes.Normalized value, so semantic (key-order-insensitive) equality
// holds against the user's jsonencode() output.
func TestMapAgentResponseToState_customToolInputSchema(t *testing.T) {
	t.Parallel()

	// API response schema: "type" first, no ExtraFields key.
	const apiInputSchema = `{"type":"object","properties":{"email":{"description":"The user's email address","type":"string"}},"required":["email"]}`

	agentJSON := `{
		"id": "agent_123",
		"name": "test",
		"model": {"id": "claude-sonnet-4-6"},
		"version": 1,
		"created_at": "2026-01-01T00:00:00Z",
		"updated_at": "2026-01-01T00:00:00Z",
		"tools": [
			{
				"type": "custom",
				"name": "lookup_user",
				"description": "Look up a user",
				"input_schema": ` + apiInputSchema + `
			}
		]
	}`

	var agent anthropic.BetaManagedAgentsAgent
	if err := json.Unmarshal([]byte(agentJSON), &agent); err != nil {
		t.Fatalf("failed to unmarshal agent fixture: %s", err)
	}

	var data AgentResourceModel
	if diags := mapAgentResponseToState(context.Background(), &agent, &data, applyMapping); diags.HasError() {
		t.Fatalf("mapAgentResponseToState returned errors: %+v", diags)
	}

	var tools []agentCustomToolModel
	if diags := data.CustomTools.ElementsAs(context.Background(), &tools, false); diags.HasError() {
		t.Fatalf("failed to read custom tools: %+v", diags)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 custom tool, got %d", len(tools))
	}

	got := tools[0].InputSchema
	if got.IsNull() || got.IsUnknown() {
		t.Fatalf("input_schema should be set, got null/unknown")
	}

	// The state value must not contain the encoding/json artifact that broke
	// round-tripping under the SDK upgrade.
	if strings.Contains(got.ValueString(), "ExtraFields") {
		t.Errorf("input_schema must not contain ExtraFields; got %q", got.ValueString())
	}

	// Semantic equality must hold against the user's jsonencode() output, which
	// orders keys alphabetically (properties, required, type) — a different order
	// than the API response. Without jsontypes.Normalized this mismatch is what
	// surfaced as "inconsistent result after apply".
	const userInputSchema = `{"properties":{"email":{"description":"The user's email address","type":"string"}},"required":["email"],"type":"object"}`
	planned := jsontypes.NewNormalizedValue(userInputSchema)
	equal, diags := planned.StringSemanticEquals(context.Background(), got)
	if diags.HasError() {
		t.Fatalf("semantic equality check returned errors: %+v", diags)
	}
	if !equal {
		t.Errorf("planned and applied input_schema should be semantically equal\nplanned: %s\napplied: %s", userInputSchema, got.ValueString())
	}
}

// customToolsList builds a custom_tools list holding one tool with schema.
func customToolsList(t *testing.T, schema string) types.List {
	t.Helper()
	tool, diags := types.ObjectValue(agentCustomToolAttrTypes, map[string]attr.Value{
		"name":         types.StringValue("lookup_user"),
		"description":  types.StringValue("Look up a user"),
		"input_schema": jsontypes.NewNormalizedValue(schema),
	})
	if diags.HasError() {
		t.Fatalf("tool: %+v", diags)
	}
	list, diags := types.ListValue(types.ObjectType{AttrTypes: agentCustomToolAttrTypes}, []attr.Value{tool})
	if diags.HasError() {
		t.Fatalf("list: %+v", diags)
	}
	return list
}

// TestBuildToolsParams_customToolInputSchemaKeepsRootKeywords: the configured
// input_schema reaches the API as is, root-level keywords other than
// type/properties/required included (additionalProperties, $defs,
// description). SDK v1.67.0 dropped them, and the SDK v1.84.0 bump kept
// dropping them on purpose until this fix. An echo of that schema then maps
// back to a value semantically equal to the configuration.
func TestBuildToolsParams_customToolInputSchemaKeepsRootKeywords(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	const configured = `{"type":"object","properties":{"address":{"$ref":"#/$defs/address"},"email":{"type":"string","description":"d"}},` +
		`"required":["email"],"additionalProperties":false,"description":"root",` +
		`"$defs":{"address":{"type":"object","properties":{"city":{"type":"string"}},"additionalProperties":false}}}`

	tools, diags := buildToolsParams(ctx, AgentResourceModel{CustomTools: customToolsList(t, configured)})
	if diags.HasError() {
		t.Fatalf("buildToolsParams: %+v", diags)
	}
	b, err := json.Marshal(tools)
	if err != nil {
		t.Fatal(err)
	}
	var sent []struct {
		InputSchema json.RawMessage `json:"input_schema"`
	}
	if err := json.Unmarshal(b, &sent); err != nil || len(sent) != 1 {
		t.Fatalf("decode request %s: %v", b, err)
	}
	assertJSONEqual(t, string(sent[0].InputSchema), configured)

	agent := parseAgentWithTools(t, `[{"type":"custom","name":"lookup_user","description":"Look up a user","input_schema":`+string(sent[0].InputSchema)+`}]`)
	var data AgentResourceModel
	if diags := mapAgentResponseToState(ctx, agent, &data, readMapping); diags.HasError() {
		t.Fatalf("mapAgentResponseToState: %+v", diags)
	}
	var mapped []agentCustomToolModel
	if diags := data.CustomTools.ElementsAs(ctx, &mapped, false); diags.HasError() || len(mapped) != 1 {
		t.Fatalf("custom tools: %+v", diags)
	}
	equal, diags := jsontypes.NewNormalizedValue(configured).StringSemanticEquals(ctx, mapped[0].InputSchema)
	if diags.HasError() || !equal {
		t.Errorf("mapped input_schema %s is not semantically equal to the configuration", mapped[0].InputSchema.ValueString())
	}
}

// A schema without `type` is still sent with `type: object`, the SDK default
// that SDK v1.67.0 applied too.
func TestBuildToolsParams_customToolInputSchemaDefaultsType(t *testing.T) {
	t.Parallel()
	tools, diags := buildToolsParams(context.Background(), AgentResourceModel{CustomTools: customToolsList(t, `{"properties":{},"additionalProperties":false}`)})
	if diags.HasError() {
		t.Fatalf("buildToolsParams: %+v", diags)
	}
	b, err := json.Marshal(tools[0].OfCustom.InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	assertJSONEqual(t, string(b), `{"type":"object","properties":{},"additionalProperties":false}`)
}

// policyVariant names the permission policy variants set on a union, "" when
// none is and a "+"-joined list when several are.
func policyVariant(allow *anthropic.BetaManagedAgentsAlwaysAllowPolicyParam, ask *anthropic.BetaManagedAgentsAlwaysAskPolicyParam, auto *anthropic.BetaManagedAgentsAutoPolicyParam) string {
	var set []string
	if allow != nil {
		set = append(set, "always_allow")
	}
	if ask != nil {
		set = append(set, "always_ask")
	}
	if auto != nil {
		set = append(set, "auto")
	}
	return strings.Join(set, "+")
}

// TestBuildAgentToolConfigParams is a regression test for the anthropic-sdk-go
// upgrade (v1.58 -> v1.67) that replaced the flat
// BetaManagedAgentsAgentToolConfigParams (a `name` enum plus shared `enabled` /
// `permission_policy` fields) with a discriminated union carrying one struct per
// built-in tool. The Terraform `name` attribute now selects the union branch, so
// this asserts every schema-accepted name lands on its own branch with the
// enabled flag and each permission policy, `auto` included, carried across.
func TestBuildAgentToolConfigParams(t *testing.T) {
	t.Parallel()

	// Each case checks the branch the name must select, and reads back the
	// enabled flag and the permission policy variant from that branch.
	tests := []struct {
		name    string
		extract func(anthropic.BetaManagedAgentsAgentToolConfigParamsUnion) (set bool, enabled param.Opt[bool], policy string)
	}{
		{"bash", func(u anthropic.BetaManagedAgentsAgentToolConfigParamsUnion) (bool, param.Opt[bool], string) {
			c := u.OfBash
			if c == nil {
				return false, param.Opt[bool]{}, ""
			}
			return true, c.Enabled, policyVariant(c.PermissionPolicy.OfAlwaysAllow, c.PermissionPolicy.OfAlwaysAsk, c.PermissionPolicy.OfAuto)
		}},
		{"edit", func(u anthropic.BetaManagedAgentsAgentToolConfigParamsUnion) (bool, param.Opt[bool], string) {
			c := u.OfEdit
			if c == nil {
				return false, param.Opt[bool]{}, ""
			}
			return true, c.Enabled, policyVariant(c.PermissionPolicy.OfAlwaysAllow, c.PermissionPolicy.OfAlwaysAsk, c.PermissionPolicy.OfAuto)
		}},
		{"read", func(u anthropic.BetaManagedAgentsAgentToolConfigParamsUnion) (bool, param.Opt[bool], string) {
			c := u.OfRead
			if c == nil {
				return false, param.Opt[bool]{}, ""
			}
			return true, c.Enabled, policyVariant(c.PermissionPolicy.OfAlwaysAllow, c.PermissionPolicy.OfAlwaysAsk, c.PermissionPolicy.OfAuto)
		}},
		{"write", func(u anthropic.BetaManagedAgentsAgentToolConfigParamsUnion) (bool, param.Opt[bool], string) {
			c := u.OfWrite
			if c == nil {
				return false, param.Opt[bool]{}, ""
			}
			return true, c.Enabled, policyVariant(c.PermissionPolicy.OfAlwaysAllow, c.PermissionPolicy.OfAlwaysAsk, c.PermissionPolicy.OfAuto)
		}},
		{"glob", func(u anthropic.BetaManagedAgentsAgentToolConfigParamsUnion) (bool, param.Opt[bool], string) {
			c := u.OfGlob
			if c == nil {
				return false, param.Opt[bool]{}, ""
			}
			return true, c.Enabled, policyVariant(c.PermissionPolicy.OfAlwaysAllow, c.PermissionPolicy.OfAlwaysAsk, c.PermissionPolicy.OfAuto)
		}},
		{"grep", func(u anthropic.BetaManagedAgentsAgentToolConfigParamsUnion) (bool, param.Opt[bool], string) {
			c := u.OfGrep
			if c == nil {
				return false, param.Opt[bool]{}, ""
			}
			return true, c.Enabled, policyVariant(c.PermissionPolicy.OfAlwaysAllow, c.PermissionPolicy.OfAlwaysAsk, c.PermissionPolicy.OfAuto)
		}},
		{"web_fetch", func(u anthropic.BetaManagedAgentsAgentToolConfigParamsUnion) (bool, param.Opt[bool], string) {
			c := u.OfWebFetch
			if c == nil {
				return false, param.Opt[bool]{}, ""
			}
			return true, c.Enabled, policyVariant(c.PermissionPolicy.OfAlwaysAllow, c.PermissionPolicy.OfAlwaysAsk, c.PermissionPolicy.OfAuto)
		}},
		{"web_search", func(u anthropic.BetaManagedAgentsAgentToolConfigParamsUnion) (bool, param.Opt[bool], string) {
			c := u.OfWebSearch
			if c == nil {
				return false, param.Opt[bool]{}, ""
			}
			return true, c.Enabled, policyVariant(c.PermissionPolicy.OfAlwaysAllow, c.PermissionPolicy.OfAlwaysAsk, c.PermissionPolicy.OfAuto)
		}},
	}

	for _, tc := range tests {
		for _, policy := range []string{"always_allow", "always_ask", "auto"} {
			t.Run(tc.name+"/"+policy, func(t *testing.T) {
				t.Parallel()

				u, diags := buildAgentToolConfigParams(tc.name, param.NewOpt(true), policy, webToolOptions{})
				if diags.HasError() {
					t.Fatalf("unexpected diagnostics: %v", diags)
				}
				set, enabled, got := tc.extract(u)
				if !set {
					t.Fatalf("name %q did not select its own union branch", tc.name)
				}
				if !enabled.Valid() || !enabled.Value {
					t.Errorf("enabled = %+v, want set to true", enabled)
				}
				if got != policy {
					t.Errorf("permission policy = %q, want %q", got, policy)
				}
			})
		}

		// An omitted permission_policy must leave every variant unset so the
		// field is elided from the request and the toolset default applies.
		t.Run(tc.name+"/no_policy", func(t *testing.T) {
			t.Parallel()

			u, diags := buildAgentToolConfigParams(tc.name, param.Opt[bool]{}, "", webToolOptions{})
			if diags.HasError() {
				t.Fatalf("unexpected diagnostics: %v", diags)
			}
			set, enabled, got := tc.extract(u)
			if !set {
				t.Fatalf("name %q did not select its own union branch", tc.name)
			}
			if enabled.Valid() {
				t.Errorf("enabled = %+v, want unset", enabled)
			}
			if got != "" {
				t.Errorf("permission policy = %q, want none", got)
			}
		})
	}
}

// TestBuildToolsParams_autoPermissionPolicy pins the request body of the `auto`
// policy (`{"type": "auto"}`) in the four places it is accepted: the agent
// toolset default, a built-in tool override, an MCP toolset default and an MCP
// tool override.
func TestBuildToolsParams_autoPermissionPolicy(t *testing.T) {
	t.Parallel()

	toolset, diags := types.ObjectValue(agentToolsetAttrTypes, map[string]attr.Value{
		"default_enabled":           types.BoolNull(),
		"default_permission_policy": types.StringValue("auto"),
		"configs": toolConfigsList(t, agentToolConfigAttrTypes,
			agentMCPToolConfigModel{Name: types.StringValue("bash"), Enabled: types.BoolNull(), PermissionPolicy: types.StringValue("always_ask")},
			agentMCPToolConfigModel{Name: types.StringValue("web_fetch"), Enabled: types.BoolNull(), PermissionPolicy: types.StringValue("auto")},
		),
	})
	if diags.HasError() {
		t.Fatalf("toolset: %+v", diags)
	}
	data := AgentResourceModel{
		AgentToolset: toolset,
		MCPToolsets: mcpToolsetsList(t, agentMCPToolsetModel{
			MCPServerName:           types.StringValue("github"),
			DefaultEnabled:          types.BoolNull(),
			DefaultPermissionPolicy: types.StringValue("auto"),
			Configs: toolConfigsList(t, agentMCPToolConfigAttrTypes,
				agentMCPToolConfigModel{Name: types.StringValue("create_issue"), Enabled: types.BoolNull(), PermissionPolicy: types.StringValue("auto")},
			),
		}),
		CustomTools: types.ListNull(types.ObjectType{AttrTypes: agentCustomToolAttrTypes}),
	}

	tools, diags := buildToolsParams(context.Background(), data)
	if diags.HasError() {
		t.Fatalf("buildToolsParams: %+v", diags)
	}
	b, err := json.Marshal(tools)
	if err != nil {
		t.Fatal(err)
	}
	const want = `[{"type":"agent_toolset_20260401","configs":[{"name":"bash","permission_policy":{"type":"always_ask"}},` +
		`{"name":"web_fetch","permission_policy":{"type":"auto"}}],"default_config":{"permission_policy":{"type":"auto"}}},` +
		`{"mcp_server_name":"github","type":"mcp_toolset","configs":[{"name":"create_issue","permission_policy":{"type":"auto"}}],` +
		`"default_config":{"permission_policy":{"type":"auto"}}}]`
	assertJSONEqual(t, string(b), want)
}

// assertJSONEqual compares two JSON documents regardless of key order.
func assertJSONEqual(t *testing.T, got, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal([]byte(got), &g); err != nil {
		t.Fatalf("unmarshal got: %s\n%s", err, got)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("unmarshal want: %s\n%s", err, want)
	}
	gb, _ := json.Marshal(g)
	wb, _ := json.Marshal(w)
	if string(gb) != string(wb) {
		t.Fatalf("got  %s\nwant %s", gb, wb)
	}
}

// TestPermissionPolicyValidator checks the values every permission policy
// attribute accepts.
func TestPermissionPolicyValidator(t *testing.T) {
	t.Parallel()

	for value, wantErr := range map[string]bool{
		"always_allow": false,
		"always_ask":   false,
		"auto":         false,
		"never":        true,
		"AUTO":         true,
	} {
		resp := &validator.StringResponse{}
		permissionPolicyValidator.ValidateString(context.Background(), validator.StringRequest{
			Path:        path.Root("permission_policy"),
			ConfigValue: types.StringValue(value),
		}, resp)
		if resp.Diagnostics.HasError() != wantErr {
			t.Errorf("%q: error = %t, want %t (%v)", value, resp.Diagnostics.HasError(), wantErr, resp.Diagnostics)
		}
	}
}

// TestBuildAgentToolConfigParams_unknownName guards the union's default branch:
// a name outside the eight built-ins must surface a diagnostic rather than
// silently produce an empty union that marshals without a discriminator.
func TestBuildAgentToolConfigParams_unknownName(t *testing.T) {
	t.Parallel()

	_, diags := buildAgentToolConfigParams("not_a_tool", param.NewOpt(true), "always_allow", webToolOptions{})
	if !diags.HasError() {
		t.Fatal("expected an error diagnostic for an unsupported tool name, got none")
	}
	if got := diags.Errors()[0].Detail(); !strings.Contains(got, "not_a_tool") {
		t.Errorf("diagnostic detail = %q, want it to name the offending value", got)
	}
}

// toolConfigsList builds a configs list in state shape from typed entries. The
// attributes attrTypes has beyond the shared three (the built-in web options)
// are null.
func toolConfigsList(t *testing.T, attrTypes map[string]attr.Type, entries ...agentMCPToolConfigModel) types.List {
	t.Helper()
	ctx := context.Background()
	objs := make([]attr.Value, 0, len(entries))
	for _, e := range entries {
		values := map[string]attr.Value{
			"name":              e.Name,
			"enabled":           e.Enabled,
			"permission_policy": e.PermissionPolicy,
		}
		for name, typ := range attrTypes {
			if _, ok := values[name]; ok {
				continue
			}
			null, err := typ.ValueFromTerraform(ctx, tftypes.NewValue(typ.TerraformType(ctx), nil))
			if err != nil {
				t.Fatalf("null %s: %s", name, err)
			}
			values[name] = null
		}
		obj, diags := types.ObjectValue(attrTypes, values)
		if diags.HasError() {
			t.Fatalf("build config object: %+v", diags)
		}
		objs = append(objs, obj)
	}
	list, diags := types.ListValue(types.ObjectType{AttrTypes: attrTypes}, objs)
	if diags.HasError() {
		t.Fatalf("build configs list: %+v", diags)
	}
	return list
}

func mcpToolsetsList(t *testing.T, toolsets ...agentMCPToolsetModel) types.List {
	t.Helper()
	objs := make([]attr.Value, 0, len(toolsets))
	for _, ts := range toolsets {
		obj, diags := types.ObjectValue(agentMCPToolsetAttrTypes, map[string]attr.Value{
			"mcp_server_name":           ts.MCPServerName,
			"default_enabled":           ts.DefaultEnabled,
			"default_permission_policy": ts.DefaultPermissionPolicy,
			"configs":                   ts.Configs,
		})
		if diags.HasError() {
			t.Fatalf("build toolset object: %+v", diags)
		}
		objs = append(objs, obj)
	}
	list, diags := types.ListValue(types.ObjectType{AttrTypes: agentMCPToolsetAttrTypes}, objs)
	if diags.HasError() {
		t.Fatalf("build toolsets list: %+v", diags)
	}
	return list
}

func parseAgentWithTools(t *testing.T, tools string) *anthropic.BetaManagedAgentsAgent {
	t.Helper()
	var agent anthropic.BetaManagedAgentsAgent
	raw := `{"id":"agent_1","name":"n","model":{"id":"claude-sonnet-4-6"},"version":1,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z","tools":` + tools + `}`
	if err := json.Unmarshal([]byte(raw), &agent); err != nil {
		t.Fatalf("unmarshal: %s", err)
	}
	return &agent
}

func mcpToolsetsFromState(t *testing.T, data AgentResourceModel) []agentMCPToolsetModel {
	t.Helper()
	var toolsets []agentMCPToolsetModel
	if diags := data.MCPToolsets.ElementsAs(context.Background(), &toolsets, false); diags.HasError() {
		t.Fatalf("read mcp_toolsets: %+v", diags)
	}
	return toolsets
}

// toolConfigsFromList reads the three attributes built-in and MCP configs share
// from a configs list of either shape.
func toolConfigsFromList(t *testing.T, list types.List) []agentMCPToolConfigModel {
	t.Helper()
	if list.IsNull() {
		return nil
	}
	configs := make([]agentMCPToolConfigModel, 0, len(list.Elements()))
	for _, el := range list.Elements() {
		obj, ok := el.(types.Object)
		if !ok {
			t.Fatalf("config element is %T, want types.Object", el)
		}
		attrs := obj.Attributes()
		configs = append(configs, agentMCPToolConfigModel{
			Name:             attrs["name"].(types.String),
			Enabled:          attrs["enabled"].(types.Bool),
			PermissionPolicy: attrs["permission_policy"].(types.String),
		})
	}
	return configs
}

// An MCP toolset declared without configs, where the live agent gained an
// always_allow per-tool config out of band, must show that config in state so
// the plan reverts it (security finding: Read used to drop it).
func TestMapMCPToolsetsToState_outOfBandConfigOnNullConfigs(t *testing.T) {
	t.Parallel()

	data := AgentResourceModel{MCPToolsets: mcpToolsetsList(t, agentMCPToolsetModel{
		MCPServerName:           types.StringValue("crm"),
		DefaultEnabled:          types.BoolValue(true),
		DefaultPermissionPolicy: types.StringValue("always_ask"),
		Configs:                 types.ListNull(types.ObjectType{AttrTypes: agentMCPToolConfigAttrTypes}),
	})}
	agent := parseAgentWithTools(t, `[{"type":"mcp_toolset","mcp_server_name":"crm",
		"default_config":{"enabled":true,"permission_policy":{"type":"always_ask"}},
		"configs":[{"name":"delete_record","enabled":true,"permission_policy":{"type":"always_allow"}}]}]`)

	if diags := mapAgentResponseToState(context.Background(), agent, &data, readMapping); diags.HasError() {
		t.Fatalf("diags: %+v", diags)
	}
	toolsets := mcpToolsetsFromState(t, data)
	if len(toolsets) != 1 {
		t.Fatalf("got %d toolsets, want 1", len(toolsets))
	}
	configs := toolConfigsFromList(t, toolsets[0].Configs)
	if len(configs) != 1 {
		t.Fatalf("got %d configs, want the out-of-band one", len(configs))
	}
	if configs[0].Name.ValueString() != "delete_record" || configs[0].PermissionPolicy.ValueString() != "always_allow" {
		t.Errorf("got config %+v, want delete_record/always_allow", configs[0])
	}
}

// A toolset the API returns for a server the configuration does not declare a
// toolset for must be appended after the tracked ones, not dropped.
func TestMapMCPToolsetsToState_outOfBandToolset(t *testing.T) {
	t.Parallel()

	data := AgentResourceModel{MCPToolsets: mcpToolsetsList(t, agentMCPToolsetModel{
		MCPServerName:           types.StringValue("crm"),
		DefaultEnabled:          types.BoolValue(true),
		DefaultPermissionPolicy: types.StringValue("always_ask"),
		Configs:                 types.ListNull(types.ObjectType{AttrTypes: agentMCPToolConfigAttrTypes}),
	})}
	agent := parseAgentWithTools(t, `[
		{"type":"mcp_toolset","mcp_server_name":"billing","default_config":{"enabled":true,"permission_policy":{"type":"always_allow"}}},
		{"type":"mcp_toolset","mcp_server_name":"crm","default_config":{"enabled":true,"permission_policy":{"type":"always_ask"}}}]`)

	if diags := mapAgentResponseToState(context.Background(), agent, &data, readMapping); diags.HasError() {
		t.Fatalf("diags: %+v", diags)
	}
	toolsets := mcpToolsetsFromState(t, data)
	if len(toolsets) != 2 {
		t.Fatalf("got %d toolsets, want 2", len(toolsets))
	}
	if toolsets[0].MCPServerName.ValueString() != "crm" || toolsets[1].MCPServerName.ValueString() != "billing" {
		t.Errorf("got order %s, %s; want the tracked toolset first, then the out-of-band one",
			toolsets[0].MCPServerName.ValueString(), toolsets[1].MCPServerName.ValueString())
	}
	if toolsets[1].DefaultPermissionPolicy.ValueString() != "always_allow" {
		t.Errorf("out-of-band toolset policy = %s, want always_allow", toolsets[1].DefaultPermissionPolicy.ValueString())
	}
}

// A field left unset in configuration stays null while the API echoes the
// toolset default it inherits, and surfaces once it diverges from it.
func TestMapToolConfigsToState_unsetFields(t *testing.T) {
	t.Parallel()

	current := toolConfigsList(t, agentMCPToolConfigAttrTypes,
		agentMCPToolConfigModel{Name: types.StringValue("search"), Enabled: types.BoolNull(), PermissionPolicy: types.StringValue("always_allow")},
		agentMCPToolConfigModel{Name: types.StringValue("delete_record"), Enabled: types.BoolValue(false), PermissionPolicy: types.StringNull()},
	)

	tests := []struct {
		name       string
		api        []apiToolConfig
		wantSearch agentMCPToolConfigModel
		wantDelete agentMCPToolConfigModel
	}{
		{
			name: "echoed defaults stay null",
			api: []apiToolConfig{
				{name: "search", enabled: true, permissionPolicy: "always_allow"},
				{name: "delete_record", enabled: false, permissionPolicy: "always_ask"},
			},
			wantSearch: agentMCPToolConfigModel{Name: types.StringValue("search"), Enabled: types.BoolNull(), PermissionPolicy: types.StringValue("always_allow")},
			wantDelete: agentMCPToolConfigModel{Name: types.StringValue("delete_record"), Enabled: types.BoolValue(false), PermissionPolicy: types.StringNull()},
		},
		{
			name: "diverging values surface",
			api: []apiToolConfig{
				{name: "search", enabled: false, permissionPolicy: "always_allow"},
				{name: "delete_record", enabled: false, permissionPolicy: "always_allow"},
			},
			wantSearch: agentMCPToolConfigModel{Name: types.StringValue("search"), Enabled: types.BoolValue(false), PermissionPolicy: types.StringValue("always_allow")},
			wantDelete: agentMCPToolConfigModel{Name: types.StringValue("delete_record"), Enabled: types.BoolValue(false), PermissionPolicy: types.StringValue("always_allow")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			list, diags := mapToolConfigsToState(current, tt.api, true, "always_ask", agentMCPToolConfigAttrTypes, readMapping)
			if diags.HasError() {
				t.Fatalf("diags: %+v", diags)
			}
			got := toolConfigsFromList(t, list)
			if len(got) != 2 {
				t.Fatalf("got %d configs, want 2", len(got))
			}
			for i, want := range []agentMCPToolConfigModel{tt.wantSearch, tt.wantDelete} {
				if !got[i].Name.Equal(want.Name) || !got[i].Enabled.Equal(want.Enabled) || !got[i].PermissionPolicy.Equal(want.PermissionPolicy) {
					t.Errorf("config %d = %+v, want %+v", i, got[i], want)
				}
			}
		})
	}
}

// A tracked config the API no longer returns is omitted (re-created on apply),
// and a config for an untracked tool is appended after the tracked ones.
func TestMapToolConfigsToState_trackedAndExtra(t *testing.T) {
	t.Parallel()

	current := toolConfigsList(t, agentToolConfigAttrTypes,
		agentMCPToolConfigModel{Name: types.StringValue("bash"), Enabled: types.BoolNull(), PermissionPolicy: types.StringValue("always_ask")},
		agentMCPToolConfigModel{Name: types.StringValue("write"), Enabled: types.BoolValue(false), PermissionPolicy: types.StringNull()},
	)
	api := []apiToolConfig{
		builtinAPIToolConfig(t, `{"type":"web_fetch","name":"web_fetch","enabled":true,"permission_policy":{"type":"always_allow"}}`),
		builtinAPIToolConfig(t, `{"type":"bash","name":"bash","enabled":true,"permission_policy":{"type":"always_ask"}}`),
	}

	list, diags := mapToolConfigsToState(current, api, true, "always_ask", agentToolConfigAttrTypes, readMapping)
	if diags.HasError() {
		t.Fatalf("diags: %+v", diags)
	}
	got := toolConfigsFromList(t, list)
	if len(got) != 2 {
		t.Fatalf("got %d configs, want bash then web_fetch", len(got))
	}
	if got[0].Name.ValueString() != "bash" || got[1].Name.ValueString() != "web_fetch" {
		t.Errorf("got %s, %s; want bash, web_fetch", got[0].Name.ValueString(), got[1].Name.ValueString())
	}
	if !got[1].Enabled.Equal(types.BoolValue(true)) || got[1].PermissionPolicy.ValueString() != "always_allow" {
		t.Errorf("extra config = %+v, want every field set from the API", got[1])
	}
}

// On Read, a config for an untracked tool whose values both equal the toolset
// defaults changes nothing, so it is skipped: if the API lists such configs, a
// toolset without `configs` would otherwise keep a diff that never clears. A
// config that differs from the defaults in either field is still reported.
func TestMapToolConfigsToState_skipsUntrackedDefaults(t *testing.T) {
	t.Parallel()

	echoed := []apiToolConfig{
		builtinAPIToolConfig(t, `{"type":"bash","name":"bash","enabled":true,"permission_policy":{"type":"always_allow"}}`),
		builtinAPIToolConfig(t, `{"type":"read","name":"read","enabled":true,"permission_policy":{"type":"always_allow"}}`),
	}
	overrides := []apiToolConfig{
		builtinAPIToolConfig(t, `{"type":"web_fetch","name":"web_fetch","enabled":true,"permission_policy":{"type":"always_ask"}}`),
		builtinAPIToolConfig(t, `{"type":"grep","name":"grep","enabled":false,"permission_policy":{"type":"always_allow"}}`),
	}
	tests := []struct {
		name    string
		current types.List
		api     []apiToolConfig
		want    []string // config names in state; nil for a null list
	}{
		{
			name:    "no configs block, only defaults listed",
			current: types.ListNull(types.ObjectType{AttrTypes: agentToolConfigAttrTypes}),
			api:     echoed,
		},
		{
			name:    "no configs block, overrides among the defaults",
			current: types.ListNull(types.ObjectType{AttrTypes: agentToolConfigAttrTypes}),
			api:     append(append([]apiToolConfig{}, echoed...), overrides...),
			want:    []string{"web_fetch", "grep"},
		},
		{
			name: "tracked config, defaults listed for other tools",
			current: toolConfigsList(t, agentToolConfigAttrTypes,
				agentMCPToolConfigModel{Name: types.StringValue("write"), Enabled: types.BoolValue(false), PermissionPolicy: types.StringNull()}),
			api:  append([]apiToolConfig{builtinAPIToolConfig(t, `{"type":"write","name":"write","enabled":false,"permission_policy":{"type":"always_allow"}}`)}, echoed...),
			want: []string{"write"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			list, diags := mapToolConfigsToState(tt.current, tt.api, true, "always_allow", agentToolConfigAttrTypes, readMapping)
			if diags.HasError() {
				t.Fatalf("diags: %+v", diags)
			}
			if tt.want == nil {
				if !list.IsNull() {
					t.Fatalf("configs = %s, want null", list)
				}
				return
			}
			got := toolConfigsFromList(t, list)
			names := make([]string, 0, len(got))
			for _, c := range got {
				names = append(names, c.Name.ValueString())
			}
			if strings.Join(names, ",") != strings.Join(tt.want, ",") {
				t.Errorf("configs = %v, want %v", names, tt.want)
			}
		})
	}
}

// The built-in agent toolset gets the same treatment as MCP toolsets: an
// override for an untracked tool is surfaced rather than filtered out.
func TestMapAgentToolsetToState_outOfBandConfig(t *testing.T) {
	t.Parallel()

	current, diags := types.ObjectValue(agentToolsetAttrTypes, map[string]attr.Value{
		"default_enabled":           types.BoolValue(true),
		"default_permission_policy": types.StringValue("always_ask"),
		"configs":                   types.ListNull(types.ObjectType{AttrTypes: agentToolConfigAttrTypes}),
	})
	if diags.HasError() {
		t.Fatalf("build toolset: %+v", diags)
	}
	data := AgentResourceModel{AgentToolset: current}
	agent := parseAgentWithTools(t, `[{"type":"agent_toolset_20260401",
		"default_config":{"enabled":true,"permission_policy":{"type":"always_ask"}},
		"configs":[{"type":"bash","name":"bash","enabled":true,"permission_policy":{"type":"always_allow"}}]}]`)

	if diags := mapAgentResponseToState(context.Background(), agent, &data, readMapping); diags.HasError() {
		t.Fatalf("diags: %+v", diags)
	}
	var toolset agentToolsetModel
	if diags := data.AgentToolset.As(context.Background(), &toolset, basetypes.ObjectAsOptions{}); diags.HasError() {
		t.Fatalf("read agent_toolset: %+v", diags)
	}
	configs := toolConfigsFromList(t, toolset.Configs)
	if len(configs) != 1 || configs[0].Name.ValueString() != "bash" || configs[0].PermissionPolicy.ValueString() != "always_allow" {
		t.Errorf("got configs %+v, want the out-of-band bash/always_allow override", configs)
	}
}

// `auto` maps like the other policies: an unset per-tool policy stays null
// while the API echoes an `auto` toolset default, and an override that
// diverges from it (here `always_allow`, set out of band) surfaces as drift.
func TestMapToolConfigsToState_autoDefault(t *testing.T) {
	t.Parallel()

	data := AgentResourceModel{MCPToolsets: mcpToolsetsList(t, agentMCPToolsetModel{
		MCPServerName:           types.StringValue("github"),
		DefaultEnabled:          types.BoolValue(true),
		DefaultPermissionPolicy: types.StringValue("auto"),
		Configs: toolConfigsList(t, agentMCPToolConfigAttrTypes,
			agentMCPToolConfigModel{Name: types.StringValue("search"), Enabled: types.BoolNull(), PermissionPolicy: types.StringNull()},
			agentMCPToolConfigModel{Name: types.StringValue("delete_repo"), Enabled: types.BoolNull(), PermissionPolicy: types.StringNull()},
		),
	})}
	agent := parseAgentWithTools(t, `[{"type":"mcp_toolset","mcp_server_name":"github",
		"default_config":{"enabled":true,"permission_policy":{"type":"auto"}},
		"configs":[{"name":"search","enabled":true,"permission_policy":{"type":"auto"}},
			{"name":"delete_repo","enabled":true,"permission_policy":{"type":"always_allow"}}]}]`)

	if diags := mapAgentResponseToState(context.Background(), agent, &data, readMapping); diags.HasError() {
		t.Fatalf("diags: %+v", diags)
	}
	toolsets := mcpToolsetsFromState(t, data)
	if len(toolsets) != 1 || toolsets[0].DefaultPermissionPolicy.ValueString() != "auto" {
		t.Fatalf("got toolsets %+v, want github with an auto default", toolsets)
	}
	configs := toolConfigsFromList(t, toolsets[0].Configs)
	if len(configs) != 2 {
		t.Fatalf("got %d configs, want 2", len(configs))
	}
	if !configs[0].PermissionPolicy.IsNull() {
		t.Errorf("search policy = %v, want null (echoed auto default)", configs[0].PermissionPolicy)
	}
	if configs[1].PermissionPolicy.ValueString() != "always_allow" {
		t.Errorf("delete_repo policy = %v, want always_allow (drift from the auto default)", configs[1].PermissionPolicy)
	}

	// The agent toolset takes `auto` the same way, and the data sources map it
	// verbatim.
	agent = parseAgentWithTools(t, `[{"type":"agent_toolset_20260401",
		"default_config":{"enabled":true,"permission_policy":{"type":"auto"}},
		"configs":[{"type":"bash","name":"bash","enabled":true,"permission_policy":{"type":"auto"}}]}]`)
	var ds AgentDataSourceModel
	if diags := mapAgentResponseToDataSource(agent, &ds); diags.HasError() {
		t.Fatalf("diags: %+v", diags)
	}
	var toolset agentToolsetModel
	if diags := ds.AgentToolset.As(context.Background(), &toolset, basetypes.ObjectAsOptions{}); diags.HasError() {
		t.Fatalf("read agent_toolset: %+v", diags)
	}
	dsConfigs := toolConfigsFromList(t, toolset.Configs)
	if toolset.DefaultPermissionPolicy.ValueString() != "auto" || len(dsConfigs) != 1 || dsConfigs[0].PermissionPolicy.ValueString() != "auto" {
		t.Errorf("data source toolset = %+v, configs %+v, want auto everywhere", toolset, dsConfigs)
	}
}

// Import (no prior state) still maps every toolset and config verbatim.
func TestMapMCPToolsetsToState_import(t *testing.T) {
	t.Parallel()

	data := AgentResourceModel{
		AgentToolset: types.ObjectNull(agentToolsetAttrTypes),
		MCPToolsets:  types.ListNull(types.ObjectType{AttrTypes: agentMCPToolsetAttrTypes}),
	}
	agent := parseAgentWithTools(t, `[{"type":"mcp_toolset","mcp_server_name":"crm",
		"default_config":{"enabled":true,"permission_policy":{"type":"always_ask"}},
		"configs":[{"name":"search","enabled":true,"permission_policy":{"type":"always_ask"}}]}]`)

	if diags := mapAgentResponseToState(context.Background(), agent, &data, readMapping); diags.HasError() {
		t.Fatalf("diags: %+v", diags)
	}
	toolsets := mcpToolsetsFromState(t, data)
	if len(toolsets) != 1 {
		t.Fatalf("got %d toolsets, want 1", len(toolsets))
	}
	configs := toolConfigsFromList(t, toolsets[0].Configs)
	if len(configs) != 1 || !configs[0].Enabled.Equal(types.BoolValue(true)) || configs[0].PermissionPolicy.ValueString() != "always_ask" {
		t.Errorf("got configs %+v, want the API config verbatim", configs)
	}
}

// unplannedTools holds tool settings that unplannedToolsPlan does not have:
// per-tool configs for agent_toolset, planned without `configs`; on crm, unset
// fields the API resolves to a value other than the toolset default (search's
// permission_policy, lookup's enabled) and a config for an unplanned tool
// (delete); a toolset for an unplanned server (billing). mcp_servers is left
// out: the tool mapping does not read it.
const unplannedTools = `[
	{"type":"agent_toolset_20260401","default_config":{"enabled":false,"permission_policy":{"type":"always_allow"}},
		"configs":[{"type":"bash","name":"bash","enabled":true,"permission_policy":{"type":"always_ask"}}]},
	{"type":"mcp_toolset","mcp_server_name":"crm","default_config":{"enabled":false,"permission_policy":{"type":"always_allow"}},
		"configs":[
			{"name":"search","enabled":true,"permission_policy":{"type":"always_ask"}},
			{"name":"lookup","enabled":true,"permission_policy":{"type":"always_ask"}},
			{"name":"delete","enabled":true,"permission_policy":{"type":"always_allow"}}]},
	{"type":"mcp_toolset","mcp_server_name":"billing","default_config":{"enabled":true,"permission_policy":{"type":"always_allow"}}}]`

// agentModelFor returns agent as a configuration without tools would hold it,
// with every list, map and object typed null so the framework can encode it.
func agentModelFor(agent *anthropic.BetaManagedAgentsAgent) AgentResourceModel {
	return AgentResourceModel{
		Model:             types.StringValue(string(agent.Model.ID)),
		Name:              types.StringValue(agent.Name),
		Metadata:          types.MapNull(types.StringType),
		MCPServers:        types.ListNull(types.ObjectType{AttrTypes: agentMCPServerAttrTypes}),
		Skills:            types.ListNull(types.ObjectType{AttrTypes: agentSkillAttrTypes}),
		CustomTools:       types.ListNull(types.ObjectType{AttrTypes: agentCustomToolAttrTypes}),
		Multiagent:        types.ObjectNull(agentMultiagentAttrTypes),
		ExecutionIdentity: types.ObjectNull(agentExecutionIdentityAttrTypes),
		AgentToolset:      types.ObjectNull(agentToolsetAttrTypes),
		MCPToolsets:       types.ListNull(types.ObjectType{AttrTypes: agentMCPToolsetAttrTypes}),
	}
}

// agentToolsetObject builds an agent_toolset value with the given defaults and
// a null `configs`.
func agentToolsetObject(t *testing.T, defaultEnabled bool, defaultPolicy string) types.Object {
	t.Helper()
	toolset, diags := types.ObjectValue(agentToolsetAttrTypes, map[string]attr.Value{
		"default_enabled":           types.BoolValue(defaultEnabled),
		"default_permission_policy": types.StringValue(defaultPolicy),
		"configs":                   types.ListNull(types.ObjectType{AttrTypes: agentToolConfigAttrTypes}),
	})
	if diags.HasError() {
		t.Fatalf("build agent_toolset: %+v", diags)
	}
	return toolset
}

// unplannedToolsPlan plans agent as it is, except for the tools.
func unplannedToolsPlan(t *testing.T, agent *anthropic.BetaManagedAgentsAgent) AgentResourceModel {
	t.Helper()
	plan := agentModelFor(agent)
	plan.AgentToolset = agentToolsetObject(t, false, "always_allow")
	plan.MCPToolsets = mcpToolsetsList(t, agentMCPToolsetModel{
		MCPServerName:           types.StringValue("crm"),
		DefaultEnabled:          types.BoolValue(false),
		DefaultPermissionPolicy: types.StringValue("always_allow"),
		Configs: toolConfigsList(t, agentMCPToolConfigAttrTypes,
			agentMCPToolConfigModel{Name: types.StringValue("search"), Enabled: types.BoolValue(true), PermissionPolicy: types.StringNull()},
			agentMCPToolConfigModel{Name: types.StringValue("lookup"), Enabled: types.BoolNull(), PermissionPolicy: types.StringValue("always_ask")},
		),
	})
	return plan
}

// Create and Update must store the planned tools: `configs` and `mcp_toolsets`
// are not Computed, so Terraform rejects any other value as "Provider produced
// inconsistent result after apply". Read, given the same response, must still
// report every unplanned setting so that it shows as drift.
func TestAgentResource_unplannedToolSettings(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	agent := parseAgentWithTools(t, unplannedTools)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, agent.RawJSON())
	}))
	t.Cleanup(srv.Close)
	r := &AgentResource{client: oauthtest.NewSDKClient(t, srv)}
	sch := schematest.ResourceSchema(t, r)

	planned := unplannedToolsPlan(t, agent)
	plan := tfsdk.Plan{Schema: sch}
	if diags := plan.Set(ctx, &planned); diags.HasError() {
		t.Fatalf("set plan: %+v", diags)
	}
	prior := planned
	prior.ID = types.StringValue(agent.ID)
	prior.Version = types.Int64Value(agent.Version)
	state := tfsdk.State{Schema: sch}
	if diags := state.Set(ctx, &prior); diags.HasError() {
		t.Fatalf("set state: %+v", diags)
	}

	stateModel := func(t *testing.T, s tfsdk.State) AgentResourceModel {
		t.Helper()
		var data AgentResourceModel
		if diags := s.Get(ctx, &data); diags.HasError() {
			t.Fatalf("get state: %+v", diags)
		}
		return data
	}
	wantPlannedTools := func(t *testing.T, s tfsdk.State) {
		t.Helper()
		data := stateModel(t, s)
		if !data.AgentToolset.Equal(planned.AgentToolset) {
			t.Errorf("agent_toolset = %s\nplanned:        %s", data.AgentToolset, planned.AgentToolset)
		}
		if !data.MCPToolsets.Equal(planned.MCPToolsets) {
			t.Errorf("mcp_toolsets = %s\nplanned:       %s", data.MCPToolsets, planned.MCPToolsets)
		}
	}

	t.Run("create keeps the plan", func(t *testing.T) {
		t.Parallel()
		resp := &resource.CreateResponse{State: tfsdk.State{Schema: sch}}
		r.Create(ctx, resource.CreateRequest{Plan: plan}, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("create: %+v", resp.Diagnostics)
		}
		wantPlannedTools(t, resp.State)
	})

	t.Run("update keeps the plan", func(t *testing.T) {
		t.Parallel()
		resp := &resource.UpdateResponse{State: tfsdk.State{Schema: sch}}
		r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("update: %+v", resp.Diagnostics)
		}
		wantPlannedTools(t, resp.State)
	})

	t.Run("read reports the drift", func(t *testing.T) {
		t.Parallel()
		resp := &resource.ReadResponse{State: state}
		r.Read(ctx, resource.ReadRequest{State: state}, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("read: %+v", resp.Diagnostics)
		}
		data := stateModel(t, resp.State)

		var toolset agentToolsetModel
		if diags := data.AgentToolset.As(ctx, &toolset, basetypes.ObjectAsOptions{}); diags.HasError() {
			t.Fatalf("read agent_toolset: %+v", diags)
		}
		if got := toolConfigsFromList(t, toolset.Configs); len(got) != 1 || got[0].Name.ValueString() != "bash" {
			t.Errorf("agent_toolset.configs = %+v, want the bash config the API returns", got)
		}
		toolsets := mcpToolsetsFromState(t, data)
		if len(toolsets) != 2 || toolsets[1].MCPServerName.ValueString() != "billing" {
			t.Fatalf("mcp_toolsets = %+v, want crm, then the unplanned billing", toolsets)
		}
		crm := toolConfigsFromList(t, toolsets[0].Configs)
		if len(crm) != 3 || crm[2].Name.ValueString() != "delete" {
			t.Fatalf("crm configs = %+v, want search, lookup, then the unplanned delete", crm)
		}
		if crm[0].PermissionPolicy.ValueString() != "always_ask" {
			t.Errorf("search.permission_policy = %s, want always_ask (differs from the toolset default)", crm[0].PermissionPolicy)
		}
		if !crm[1].Enabled.Equal(types.BoolValue(true)) {
			t.Errorf("lookup.enabled = %s, want true (differs from the toolset default)", crm[1].Enabled)
		}
	})
}

// defaultConfigsTools gives each toolset one per-tool config that only repeats
// the toolset defaults.
const defaultConfigsTools = `[
	{"type":"agent_toolset_20260401","default_config":{"enabled":true,"permission_policy":{"type":"always_allow"}},
		"configs":[{"name":"bash","enabled":true,"permission_policy":{"type":"always_allow"}}]},
	{"type":"mcp_toolset","mcp_server_name":"crm","default_config":{"enabled":true,"permission_policy":{"type":"always_ask"}},
		"configs":[{"name":"search","enabled":true,"permission_policy":{"type":"always_ask"}}]}]`

// On import, state holds no toolset to tell an echoed default from a config
// the configuration lists, so Read maps every per-tool config verbatim: a
// configuration that lists one matches state right after import, and
// ImportStateVerify holds. For a toolset state already holds with `configs`
// null, the same configs change nothing and are skipped.
func TestAgentResource_readConfigsEqualToDefaults(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	agent := parseAgentWithTools(t, defaultConfigsTools)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, agent.RawJSON())
	}))
	t.Cleanup(srv.Close)
	r := &AgentResource{client: oauthtest.NewSDKClient(t, srv)}
	sch := schematest.ResourceSchema(t, r)

	read := func(t *testing.T, state tfsdk.State) (agentConfigs, crmConfigs types.List) {
		t.Helper()
		resp := &resource.ReadResponse{State: state}
		r.Read(ctx, resource.ReadRequest{State: state}, resp)
		if resp.Diagnostics.HasError() {
			t.Fatalf("read: %+v", resp.Diagnostics)
		}
		var data AgentResourceModel
		if diags := resp.State.Get(ctx, &data); diags.HasError() {
			t.Fatalf("get state: %+v", diags)
		}
		var toolset agentToolsetModel
		if diags := data.AgentToolset.As(ctx, &toolset, basetypes.ObjectAsOptions{}); diags.HasError() {
			t.Fatalf("read agent_toolset: %+v", diags)
		}
		toolsets := mcpToolsetsFromState(t, data)
		if len(toolsets) != 1 {
			t.Fatalf("mcp_toolsets = %+v, want crm", toolsets)
		}
		return toolset.Configs, toolsets[0].Configs
	}

	t.Run("import maps them verbatim", func(t *testing.T) {
		t.Parallel()
		vals := schematest.NullValues(t, r)
		vals["id"] = tftypes.NewValue(tftypes.String, agent.ID)
		agentList, crmList := read(t, tfsdk.State{Schema: sch, Raw: tftypes.NewValue(schematest.ResourceObjectType(t, r), vals)})
		agentConfigs, crmConfigs := toolConfigsFromList(t, agentList), toolConfigsFromList(t, crmList)

		if len(agentConfigs) != 1 || agentConfigs[0].Name.ValueString() != "bash" ||
			!agentConfigs[0].Enabled.Equal(types.BoolValue(true)) || agentConfigs[0].PermissionPolicy.ValueString() != "always_allow" {
			t.Errorf("agent_toolset.configs = %+v, want bash verbatim", agentConfigs)
		}
		if len(crmConfigs) != 1 || crmConfigs[0].Name.ValueString() != "search" ||
			!crmConfigs[0].Enabled.Equal(types.BoolValue(true)) || crmConfigs[0].PermissionPolicy.ValueString() != "always_ask" {
			t.Errorf("crm configs = %+v, want search verbatim", crmConfigs)
		}
	})

	t.Run("read after an apply without configs skips them", func(t *testing.T) {
		t.Parallel()
		prior := agentModelFor(agent)
		prior.ID = types.StringValue(agent.ID)
		prior.AgentToolset = agentToolsetObject(t, true, "always_allow")
		prior.MCPToolsets = mcpToolsetsList(t, agentMCPToolsetModel{
			MCPServerName:           types.StringValue("crm"),
			DefaultEnabled:          types.BoolValue(true),
			DefaultPermissionPolicy: types.StringValue("always_ask"),
			Configs:                 types.ListNull(types.ObjectType{AttrTypes: agentMCPToolConfigAttrTypes}),
		})
		state := tfsdk.State{Schema: sch}
		if diags := state.Set(ctx, &prior); diags.HasError() {
			t.Fatalf("set state: %+v", diags)
		}

		if agentList, crmList := read(t, state); !agentList.IsNull() || !crmList.IsNull() {
			t.Errorf("agent_toolset.configs = %s, crm configs = %s; want both null", agentList, crmList)
		}
	})
}

// On Create and Update an mcp_toolsets list the plan leaves out stays null even
// if the API returns a toolset; Read (here, import) reports it.
func TestMapMCPToolsetsToState_applyKeepsNullPlan(t *testing.T) {
	t.Parallel()

	agent := parseAgentWithTools(t, `[{"type":"mcp_toolset","mcp_server_name":"crm",
		"default_config":{"enabled":true,"permission_policy":{"type":"always_allow"}}}]`)
	for mode, wantNull := range map[mappingMode]bool{applyMapping: true, readMapping: false} {
		data := AgentResourceModel{MCPToolsets: types.ListNull(types.ObjectType{AttrTypes: agentMCPToolsetAttrTypes})}
		if diags := mapAgentResponseToState(context.Background(), agent, &data, mode); diags.HasError() {
			t.Fatalf("mode %d: %+v", mode, diags)
		}
		if data.MCPToolsets.IsNull() != wantNull {
			t.Errorf("mode %d: mcp_toolsets = %s, want null: %t", mode, data.MCPToolsets, wantNull)
		}
	}
}

// Two mcp_toolsets entries for the same server fail at plan time: state keeps
// one entry per server, so the apply would end in an inconsistent result.
func TestUniqueMCPServerNamesValidator(t *testing.T) {
	t.Parallel()

	a, ok := schematest.ResourceSchema(t, NewAgentResource()).Attributes["mcp_toolsets"].(interface{ ListValidators() []validator.List })
	if !ok {
		t.Fatal("mcp_toolsets is not a list attribute")
	}
	wired := false
	for _, v := range a.ListValidators() {
		_, isUnique := v.(uniqueMCPServerNamesValidator)
		wired = wired || isUnique
	}
	if !wired {
		t.Error("mcp_toolsets does not use uniqueMCPServerNamesValidator")
	}

	elemType := types.ObjectType{AttrTypes: agentMCPToolsetAttrTypes}
	toolset := func(name types.String) attr.Value {
		return types.ObjectValueMust(agentMCPToolsetAttrTypes, map[string]attr.Value{
			"mcp_server_name":           name,
			"default_enabled":           types.BoolNull(),
			"default_permission_policy": types.StringNull(),
			"configs":                   types.ListNull(types.ObjectType{AttrTypes: agentMCPToolConfigAttrTypes}),
		})
	}
	tests := []struct {
		name     string
		value    types.List
		wantPath string // path of the expected error; empty when none is expected
	}{
		{name: "null", value: types.ListNull(elemType)},
		{name: "distinct, with unknown values", value: types.ListValueMust(elemType, []attr.Value{
			toolset(types.StringValue("crm")), toolset(types.StringUnknown()), types.ObjectUnknown(agentMCPToolsetAttrTypes), toolset(types.StringValue("billing")),
		})},
		{name: "duplicate", value: types.ListValueMust(elemType, []attr.Value{
			toolset(types.StringValue("crm")), toolset(types.StringValue("billing")), toolset(types.StringValue("crm")),
		}), wantPath: "mcp_toolsets[2].mcp_server_name"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp := &validator.ListResponse{}
			uniqueMCPServerNamesValidator{}.ValidateList(context.Background(),
				validator.ListRequest{Path: path.Root("mcp_toolsets"), ConfigValue: tt.value}, resp)
			if tt.wantPath == "" {
				if resp.Diagnostics.HasError() {
					t.Fatalf("unexpected diagnostics: %+v", resp.Diagnostics)
				}
				return
			}
			if resp.Diagnostics.ErrorsCount() != 1 {
				t.Fatalf("got %+v, want one error", resp.Diagnostics)
			}
			d := resp.Diagnostics.Errors()[0]
			withPath, ok := d.(diag.DiagnosticWithPath)
			if !ok || withPath.Path().String() != tt.wantPath {
				t.Errorf("error %+v, want it on %s", d, tt.wantPath)
			}
			if !strings.Contains(d.Detail(), `"crm" is set in entries 0 and 2`) {
				t.Errorf("detail = %q, want it to name the server and both entries", d.Detail())
			}
		})
	}
}

func modelParamsJSON(t *testing.T, m anthropic.BetaManagedAgentsModelConfigParams) map[string]any {
	t.Helper()
	raw, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal model params: %s", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal model params: %s", err)
	}
	return out
}

func TestBuildModelConfigParams(t *testing.T) {
	t.Parallel()

	base := func(effort, geo types.String) AgentResourceModel {
		return AgentResourceModel{
			Model:             types.StringValue("claude-sonnet-4-6"),
			ModelSpeed:        types.StringNull(),
			ModelEffort:       effort,
			ModelInferenceGeo: geo,
		}
	}

	tests := []struct {
		name string
		plan AgentResourceModel
		want map[string]any
	}{
		{
			name: "create with effort and geo",
			plan: base(types.StringValue("high"), types.StringValue("us")),
			want: map[string]any{"id": "claude-sonnet-4-6", "effort": "high", "inference_geo": "us"},
		},
		{
			name: "create with unknown effort omits it",
			plan: base(types.StringUnknown(), types.StringNull()),
			want: map[string]any{"id": "claude-sonnet-4-6"},
		},
		{
			name: "update sends planned effort",
			plan: base(types.StringValue("low"), types.StringNull()),
			want: map[string]any{"id": "claude-sonnet-4-6", "effort": "low"},
		},
		{
			name: "update with null geo omits it so the pin is cleared",
			plan: base(types.StringValue("high"), types.StringNull()),
			want: map[string]any{"id": "claude-sonnet-4-6", "effort": "high"},
		},
		{
			name: "update keeps unchanged geo",
			plan: base(types.StringValue("high"), types.StringValue("us")),
			want: map[string]any{"id": "claude-sonnet-4-6", "effort": "high", "inference_geo": "us"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := modelParamsJSON(t, buildModelConfigParams(tt.plan))
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("%s: got %v, want %v (full: %v)", k, got[k], v, got)
				}
			}
		})
	}
}

func TestMapAgentResponseToState_modelEffortAndGeo(t *testing.T) {
	t.Parallel()

	t.Run("present", func(t *testing.T) {
		t.Parallel()
		data := AgentResourceModel{ModelEffort: types.StringUnknown()}
		agent := parseAgentFixture(t, `{"id":"claude-sonnet-4-6","effort":{"type":"xhigh"},"inference_geo":"us"}`)
		if diags := mapAgentResponseToState(context.Background(), agent, &data, readMapping); diags.HasError() {
			t.Fatalf("diags: %+v", diags)
		}
		if data.ModelEffort.ValueString() != "xhigh" || data.ModelInferenceGeo.ValueString() != "us" {
			t.Errorf("got effort=%v geo=%v", data.ModelEffort, data.ModelInferenceGeo)
		}
	})

	t.Run("absent resolves unknown to null", func(t *testing.T) {
		t.Parallel()
		data := AgentResourceModel{ModelEffort: types.StringUnknown()}
		agent := parseAgentFixture(t, `{"id":"claude-sonnet-4-6"}`)
		if diags := mapAgentResponseToState(context.Background(), agent, &data, readMapping); diags.HasError() {
			t.Fatalf("diags: %+v", diags)
		}
		if !data.ModelEffort.IsNull() || !data.ModelInferenceGeo.IsNull() {
			t.Errorf("got effort=%v geo=%v, want null", data.ModelEffort, data.ModelInferenceGeo)
		}
	})

	t.Run("empty and null map to null", func(t *testing.T) {
		t.Parallel()
		for _, model := range []string{
			`{"id":"claude-sonnet-4-6","effort":null,"inference_geo":""}`,
			`{"id":"claude-sonnet-4-6","inference_geo":null}`,
		} {
			data := AgentResourceModel{}
			if diags := mapAgentResponseToState(context.Background(), parseAgentFixture(t, model), &data, readMapping); diags.HasError() {
				t.Fatalf("diags: %+v", diags)
			}
			if !data.ModelEffort.IsNull() || !data.ModelInferenceGeo.IsNull() {
				t.Errorf("%s: got effort=%v geo=%v, want null", model, data.ModelEffort, data.ModelInferenceGeo)
			}
		}
	})
}

// builtinAPIToolConfig decodes one built-in tool config as the API returns it.
func builtinAPIToolConfig(t *testing.T, raw string) apiToolConfig {
	t.Helper()
	var c anthropic.BetaManagedAgentsAgentToolConfigUnion
	if err := json.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("unmarshal tool config: %s", err)
	}
	cfg, diags := apiAgentToolConfig(c)
	if diags.HasError() {
		t.Fatalf("apiAgentToolConfig: %+v", diags)
	}
	return cfg
}

// agentServer answers every request with agent, as the create, read and
// update endpoints would.
func agentServer(t *testing.T, agent *anthropic.BetaManagedAgentsAgent) *AgentResource {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, agent.RawJSON())
	}))
	t.Cleanup(srv.Close)
	return &AgentResource{client: oauthtest.NewSDKClient(t, srv)}
}

// plannedAgent plans agent as it is, except for the toolsets.
func plannedAgent(agent *anthropic.BetaManagedAgentsAgent, toolset types.Object, mcpToolsets types.List) AgentResourceModel {
	return AgentResourceModel{
		Model:             types.StringValue(string(agent.Model.ID)),
		Name:              types.StringValue(agent.Name),
		Metadata:          types.MapNull(types.StringType),
		MCPServers:        types.ListNull(types.ObjectType{AttrTypes: agentMCPServerAttrTypes}),
		Skills:            types.ListNull(types.ObjectType{AttrTypes: agentSkillAttrTypes}),
		CustomTools:       types.ListNull(types.ObjectType{AttrTypes: agentCustomToolAttrTypes}),
		Multiagent:        types.ObjectNull(agentMultiagentAttrTypes),
		ExecutionIdentity: types.ObjectNull(agentExecutionIdentityAttrTypes),
		AgentToolset:      toolset,
		MCPToolsets:       mcpToolsets,
	}
}

// applyAndRead runs Create and Update with planned as the plan, and Read with
// planned as the prior state, all against r, and returns the three states.
func applyAndRead(t *testing.T, r *AgentResource, agent *anthropic.BetaManagedAgentsAgent, planned AgentResourceModel) (created, updated, read AgentResourceModel) {
	t.Helper()
	ctx := context.Background()
	sch := schematest.ResourceSchema(t, r)

	plan := tfsdk.Plan{Schema: sch}
	if diags := plan.Set(ctx, &planned); diags.HasError() {
		t.Fatalf("set plan: %+v", diags)
	}
	prior := planned
	prior.ID = types.StringValue(agent.ID)
	prior.Version = types.Int64Value(agent.Version)
	state := tfsdk.State{Schema: sch}
	if diags := state.Set(ctx, &prior); diags.HasError() {
		t.Fatalf("set state: %+v", diags)
	}
	model := func(s tfsdk.State) AgentResourceModel {
		var data AgentResourceModel
		if diags := s.Get(ctx, &data); diags.HasError() {
			t.Fatalf("get state: %+v", diags)
		}
		return data
	}

	createResp := &resource.CreateResponse{State: tfsdk.State{Schema: sch}}
	r.Create(ctx, resource.CreateRequest{Plan: plan}, createResp)
	if createResp.Diagnostics.HasError() {
		t.Fatalf("create: %+v", createResp.Diagnostics)
	}
	updateResp := &resource.UpdateResponse{State: tfsdk.State{Schema: sch}}
	r.Update(ctx, resource.UpdateRequest{Plan: plan, State: state}, updateResp)
	if updateResp.Diagnostics.HasError() {
		t.Fatalf("update: %+v", updateResp.Diagnostics)
	}
	readResp := &resource.ReadResponse{State: state}
	r.Read(ctx, resource.ReadRequest{State: state}, readResp)
	if readResp.Diagnostics.HasError() {
		t.Fatalf("read: %+v", readResp.Diagnostics)
	}
	return model(createResp.State), model(updateResp.State), model(readResp.State)
}
