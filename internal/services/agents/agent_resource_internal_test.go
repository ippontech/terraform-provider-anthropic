// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	rschema "github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
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

// TestBuildAgentToolConfigParams is a regression test for the anthropic-sdk-go
// upgrade (v1.58 -> v1.67) that replaced the flat
// BetaManagedAgentsAgentToolConfigParams (a `name` enum plus shared `enabled` /
// `permission_policy` fields) with a discriminated union carrying one struct per
// built-in tool. The Terraform `name` attribute now selects the union branch, so
// this asserts every schema-accepted name lands on its own branch with the
// enabled flag and permission policy carried across.
func TestBuildAgentToolConfigParams(t *testing.T) {
	t.Parallel()

	// Each case checks the branch the name must select, and reads back the
	// enabled/permission-policy fields from that branch.
	tests := []struct {
		name    string
		extract func(anthropic.BetaManagedAgentsAgentToolConfigParamsUnion) (set bool, enabled param.Opt[bool], allow bool, ask bool)
	}{
		{"bash", func(u anthropic.BetaManagedAgentsAgentToolConfigParamsUnion) (bool, param.Opt[bool], bool, bool) {
			c := u.OfBash
			if c == nil {
				return false, param.Opt[bool]{}, false, false
			}
			return true, c.Enabled, c.PermissionPolicy.OfAlwaysAllow != nil, c.PermissionPolicy.OfAlwaysAsk != nil
		}},
		{"edit", func(u anthropic.BetaManagedAgentsAgentToolConfigParamsUnion) (bool, param.Opt[bool], bool, bool) {
			c := u.OfEdit
			if c == nil {
				return false, param.Opt[bool]{}, false, false
			}
			return true, c.Enabled, c.PermissionPolicy.OfAlwaysAllow != nil, c.PermissionPolicy.OfAlwaysAsk != nil
		}},
		{"read", func(u anthropic.BetaManagedAgentsAgentToolConfigParamsUnion) (bool, param.Opt[bool], bool, bool) {
			c := u.OfRead
			if c == nil {
				return false, param.Opt[bool]{}, false, false
			}
			return true, c.Enabled, c.PermissionPolicy.OfAlwaysAllow != nil, c.PermissionPolicy.OfAlwaysAsk != nil
		}},
		{"write", func(u anthropic.BetaManagedAgentsAgentToolConfigParamsUnion) (bool, param.Opt[bool], bool, bool) {
			c := u.OfWrite
			if c == nil {
				return false, param.Opt[bool]{}, false, false
			}
			return true, c.Enabled, c.PermissionPolicy.OfAlwaysAllow != nil, c.PermissionPolicy.OfAlwaysAsk != nil
		}},
		{"glob", func(u anthropic.BetaManagedAgentsAgentToolConfigParamsUnion) (bool, param.Opt[bool], bool, bool) {
			c := u.OfGlob
			if c == nil {
				return false, param.Opt[bool]{}, false, false
			}
			return true, c.Enabled, c.PermissionPolicy.OfAlwaysAllow != nil, c.PermissionPolicy.OfAlwaysAsk != nil
		}},
		{"grep", func(u anthropic.BetaManagedAgentsAgentToolConfigParamsUnion) (bool, param.Opt[bool], bool, bool) {
			c := u.OfGrep
			if c == nil {
				return false, param.Opt[bool]{}, false, false
			}
			return true, c.Enabled, c.PermissionPolicy.OfAlwaysAllow != nil, c.PermissionPolicy.OfAlwaysAsk != nil
		}},
		{"web_fetch", func(u anthropic.BetaManagedAgentsAgentToolConfigParamsUnion) (bool, param.Opt[bool], bool, bool) {
			c := u.OfWebFetch
			if c == nil {
				return false, param.Opt[bool]{}, false, false
			}
			return true, c.Enabled, c.PermissionPolicy.OfAlwaysAllow != nil, c.PermissionPolicy.OfAlwaysAsk != nil
		}},
		{"web_search", func(u anthropic.BetaManagedAgentsAgentToolConfigParamsUnion) (bool, param.Opt[bool], bool, bool) {
			c := u.OfWebSearch
			if c == nil {
				return false, param.Opt[bool]{}, false, false
			}
			return true, c.Enabled, c.PermissionPolicy.OfAlwaysAllow != nil, c.PermissionPolicy.OfAlwaysAsk != nil
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name+"/always_allow", func(t *testing.T) {
			t.Parallel()

			u, diags := buildAgentToolConfigParams(tc.name, param.NewOpt(true), "always_allow")
			if diags.HasError() {
				t.Fatalf("unexpected diagnostics: %v", diags)
			}
			set, enabled, allow, ask := tc.extract(u)
			if !set {
				t.Fatalf("name %q did not select its own union branch", tc.name)
			}
			if !enabled.Valid() || !enabled.Value {
				t.Errorf("enabled = %+v, want set to true", enabled)
			}
			if !allow || ask {
				t.Errorf("permission policy = (allow=%t, ask=%t), want (true, false)", allow, ask)
			}
		})

		t.Run(tc.name+"/always_ask", func(t *testing.T) {
			t.Parallel()

			u, diags := buildAgentToolConfigParams(tc.name, param.NewOpt(false), "always_ask")
			if diags.HasError() {
				t.Fatalf("unexpected diagnostics: %v", diags)
			}
			set, enabled, allow, ask := tc.extract(u)
			if !set {
				t.Fatalf("name %q did not select its own union branch", tc.name)
			}
			if !enabled.Valid() || enabled.Value {
				t.Errorf("enabled = %+v, want set to false", enabled)
			}
			if allow || !ask {
				t.Errorf("permission policy = (allow=%t, ask=%t), want (false, true)", allow, ask)
			}
		})

		// An omitted permission_policy must leave both variants unset so the
		// field is elided from the request and the toolset default applies.
		t.Run(tc.name+"/no_policy", func(t *testing.T) {
			t.Parallel()

			u, diags := buildAgentToolConfigParams(tc.name, param.Opt[bool]{}, "")
			if diags.HasError() {
				t.Fatalf("unexpected diagnostics: %v", diags)
			}
			set, enabled, allow, ask := tc.extract(u)
			if !set {
				t.Fatalf("name %q did not select its own union branch", tc.name)
			}
			if enabled.Valid() {
				t.Errorf("enabled = %+v, want unset", enabled)
			}
			if allow || ask {
				t.Errorf("permission policy = (allow=%t, ask=%t), want both unset", allow, ask)
			}
		})
	}
}

// TestBuildAgentToolConfigParams_unknownName guards the union's default branch:
// a name outside the eight built-ins must surface a diagnostic rather than
// silently produce an empty union that marshals without a discriminator.
func TestBuildAgentToolConfigParams_unknownName(t *testing.T) {
	t.Parallel()

	_, diags := buildAgentToolConfigParams("not_a_tool", param.NewOpt(true), "always_allow")
	if !diags.HasError() {
		t.Fatal("expected an error diagnostic for an unsupported tool name, got none")
	}
	if got := diags.Errors()[0].Detail(); !strings.Contains(got, "not_a_tool") {
		t.Errorf("diagnostic detail = %q, want it to name the offending value", got)
	}
}

// toolConfigsList builds a configs list in state shape from typed entries.
func toolConfigsList(t *testing.T, attrTypes map[string]attr.Type, entries ...agentToolConfigModel) types.List {
	t.Helper()
	objs := make([]attr.Value, 0, len(entries))
	for _, e := range entries {
		obj, diags := types.ObjectValue(attrTypes, map[string]attr.Value{
			"name":              e.Name,
			"enabled":           e.Enabled,
			"permission_policy": e.PermissionPolicy,
		})
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

func toolConfigsFromList(t *testing.T, list types.List) []agentToolConfigModel {
	t.Helper()
	if list.IsNull() {
		return nil
	}
	var configs []agentToolConfigModel
	if diags := list.ElementsAs(context.Background(), &configs, false); diags.HasError() {
		t.Fatalf("read configs: %+v", diags)
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
		agentToolConfigModel{Name: types.StringValue("search"), Enabled: types.BoolNull(), PermissionPolicy: types.StringValue("always_allow")},
		agentToolConfigModel{Name: types.StringValue("delete_record"), Enabled: types.BoolValue(false), PermissionPolicy: types.StringNull()},
	)

	tests := []struct {
		name       string
		api        []apiToolConfig
		wantSearch agentToolConfigModel
		wantDelete agentToolConfigModel
	}{
		{
			name: "echoed defaults stay null",
			api: []apiToolConfig{
				{name: "search", enabled: true, permissionPolicy: "always_allow"},
				{name: "delete_record", enabled: false, permissionPolicy: "always_ask"},
			},
			wantSearch: agentToolConfigModel{Name: types.StringValue("search"), Enabled: types.BoolNull(), PermissionPolicy: types.StringValue("always_allow")},
			wantDelete: agentToolConfigModel{Name: types.StringValue("delete_record"), Enabled: types.BoolValue(false), PermissionPolicy: types.StringNull()},
		},
		{
			name: "diverging values surface",
			api: []apiToolConfig{
				{name: "search", enabled: false, permissionPolicy: "always_allow"},
				{name: "delete_record", enabled: false, permissionPolicy: "always_allow"},
			},
			wantSearch: agentToolConfigModel{Name: types.StringValue("search"), Enabled: types.BoolValue(false), PermissionPolicy: types.StringValue("always_allow")},
			wantDelete: agentToolConfigModel{Name: types.StringValue("delete_record"), Enabled: types.BoolValue(false), PermissionPolicy: types.StringValue("always_allow")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			list, diags := mapToolConfigsToState(context.Background(), current, tt.api, true, "always_ask", agentMCPToolConfigAttrTypes, readMapping)
			if diags.HasError() {
				t.Fatalf("diags: %+v", diags)
			}
			got := toolConfigsFromList(t, list)
			if len(got) != 2 {
				t.Fatalf("got %d configs, want 2", len(got))
			}
			for i, want := range []agentToolConfigModel{tt.wantSearch, tt.wantDelete} {
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
		agentToolConfigModel{Name: types.StringValue("bash"), Enabled: types.BoolNull(), PermissionPolicy: types.StringValue("always_ask")},
		agentToolConfigModel{Name: types.StringValue("write"), Enabled: types.BoolValue(false), PermissionPolicy: types.StringNull()},
	)
	api := []apiToolConfig{
		{name: "web_fetch", enabled: true, permissionPolicy: "always_allow"},
		{name: "bash", enabled: true, permissionPolicy: "always_ask"},
	}

	list, diags := mapToolConfigsToState(context.Background(), current, api, true, "always_ask", agentToolConfigAttrTypes, readMapping)
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
		{name: "bash", enabled: true, permissionPolicy: "always_allow"},
		{name: "read", enabled: true, permissionPolicy: "always_allow"},
	}
	overrides := []apiToolConfig{
		{name: "web_fetch", enabled: true, permissionPolicy: "always_ask"},
		{name: "grep", enabled: false, permissionPolicy: "always_allow"},
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
				agentToolConfigModel{Name: types.StringValue("write"), Enabled: types.BoolValue(false), PermissionPolicy: types.StringNull()}),
			api:  append([]apiToolConfig{{name: "write", enabled: false, permissionPolicy: "always_allow"}}, echoed...),
			want: []string{"write"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			list, diags := mapToolConfigsToState(context.Background(), tt.current, tt.api, true, "always_allow", agentToolConfigAttrTypes, readMapping)
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
		"configs":[{"name":"bash","enabled":true,"permission_policy":{"type":"always_ask"}}]},
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
		Model:        types.StringValue(string(agent.Model.ID)),
		Name:         types.StringValue(agent.Name),
		Metadata:     types.MapNull(types.StringType),
		MCPServers:   types.ListNull(types.ObjectType{AttrTypes: agentMCPServerAttrTypes}),
		Skills:       types.ListNull(types.ObjectType{AttrTypes: agentSkillAttrTypes}),
		CustomTools:  types.ListNull(types.ObjectType{AttrTypes: agentCustomToolAttrTypes}),
		Multiagent:   types.ObjectNull(agentMultiagentAttrTypes),
		AgentToolset: types.ObjectNull(agentToolsetAttrTypes),
		MCPToolsets:  types.ListNull(types.ObjectType{AttrTypes: agentMCPToolsetAttrTypes}),
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
			agentToolConfigModel{Name: types.StringValue("search"), Enabled: types.BoolValue(true), PermissionPolicy: types.StringNull()},
			agentToolConfigModel{Name: types.StringValue("lookup"), Enabled: types.BoolNull(), PermissionPolicy: types.StringValue("always_ask")},
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

// agentTestProvider serves a single AgentResource, so a test can drive it
// through the framework's protocol server: PlanResourceChange then applies the
// schema defaults, marks unconfigured Computed values unknown and runs the plan
// modifiers as it does for Terraform. ConfigureProvider is never called, so the
// resource keeps the client the test gave it.
type agentTestProvider struct{ r *AgentResource }

func (agentTestProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "anthropic"
}

func (agentTestProvider) Schema(context.Context, provider.SchemaRequest, *provider.SchemaResponse) {}

func (agentTestProvider) Configure(context.Context, provider.ConfigureRequest, *provider.ConfigureResponse) {
}

func (p agentTestProvider) Resources(context.Context) []func() resource.Resource {
	return []func() resource.Resource{func() resource.Resource { return p.r }}
}

func (agentTestProvider) DataSources(context.Context) []func() datasource.DataSource { return nil }

// agentProtocol runs ReadResource, PlanResourceChange and ApplyResourceChange
// for anthropic_agent, with values in their Terraform encoding.
type agentProtocol struct {
	t      *testing.T
	server tfprotov6.ProviderServer
	sch    rschema.Schema
	typ    tftypes.Type
}

func newAgentProtocol(t *testing.T, r *AgentResource) agentProtocol {
	t.Helper()
	server, err := providerserver.NewProtocol6WithError(agentTestProvider{r: r})()
	if err != nil {
		t.Fatalf("provider server: %s", err)
	}
	return agentProtocol{t: t, server: server, sch: schematest.ResourceSchema(t, r), typ: schematest.ResourceObjectType(t, r)}
}

// value encodes m as Terraform sends it: a configuration, a prior state or a
// proposed new state.
func (p agentProtocol) value(m AgentResourceModel) tftypes.Value {
	p.t.Helper()
	s := tfsdk.State{Schema: p.sch}
	if diags := s.Set(context.Background(), &m); diags.HasError() {
		p.t.Fatalf("encode: %+v", diags)
	}
	return s.Raw
}

// model decodes v, unknown values included.
func (p agentProtocol) model(v tftypes.Value) AgentResourceModel {
	p.t.Helper()
	var m AgentResourceModel
	if diags := (tfsdk.Plan{Schema: p.sch, Raw: v}).Get(context.Background(), &m); diags.HasError() {
		p.t.Fatalf("decode: %+v", diags)
	}
	return m
}

func (p agentProtocol) dynamic(v tftypes.Value) *tfprotov6.DynamicValue {
	p.t.Helper()
	dv, err := tfprotov6.NewDynamicValue(p.typ, v)
	if err != nil {
		p.t.Fatalf("dynamic value: %s", err)
	}
	return &dv
}

func (p agentProtocol) decode(dv *tfprotov6.DynamicValue, diags []*tfprotov6.Diagnostic) tftypes.Value {
	p.t.Helper()
	for _, d := range diags {
		if d.Severity == tfprotov6.DiagnosticSeverityError {
			p.t.Fatalf("%s: %s", d.Summary, d.Detail)
		}
	}
	v, err := dv.Unmarshal(p.typ)
	if err != nil {
		p.t.Fatalf("unmarshal: %s", err)
	}
	return v
}

func (p agentProtocol) read(state tftypes.Value) tftypes.Value {
	p.t.Helper()
	resp, err := p.server.ReadResource(context.Background(), &tfprotov6.ReadResourceRequest{
		TypeName: "anthropic_agent", CurrentState: p.dynamic(state),
	})
	if err != nil {
		p.t.Fatalf("read: %s", err)
	}
	return p.decode(resp.NewState, resp.Diagnostics)
}

// plan returns the planned state. proposed is what Terraform derives from
// prior and config: the configured values, and the prior value of every
// Computed attribute the configuration leaves null.
func (p agentProtocol) plan(prior, config, proposed tftypes.Value) tftypes.Value {
	p.t.Helper()
	resp, err := p.server.PlanResourceChange(context.Background(), &tfprotov6.PlanResourceChangeRequest{
		TypeName: "anthropic_agent", PriorState: p.dynamic(prior), Config: p.dynamic(config), ProposedNewState: p.dynamic(proposed),
	})
	if err != nil {
		p.t.Fatalf("plan: %s", err)
	}
	return p.decode(resp.PlannedState, resp.Diagnostics)
}

func (p agentProtocol) apply(prior, config, planned tftypes.Value) tftypes.Value {
	p.t.Helper()
	resp, err := p.server.ApplyResourceChange(context.Background(), &tfprotov6.ApplyResourceChangeRequest{
		TypeName: "anthropic_agent", PriorState: p.dynamic(prior), Config: p.dynamic(config), PlannedState: p.dynamic(planned),
	})
	if err != nil {
		p.t.Fatalf("apply: %s", err)
	}
	return p.decode(resp.NewState, resp.Diagnostics)
}

// toolsetDefaults holds the four default_config attributes of
// toolsetDefaultsModel: agent_toolset's, then those of the crm MCP toolset.
type toolsetDefaults struct {
	agentEnabled types.Bool
	agentPolicy  types.String
	mcpEnabled   types.Bool
	mcpPolicy    types.String
}

// unsetDefaults is a configuration that sets none of them.
var unsetDefaults = toolsetDefaults{types.BoolNull(), types.StringNull(), types.BoolNull(), types.StringNull()}

// documentedDefaults are the values the API documents for an omitted
// default_config, which a state written before the schema defaults holds.
var documentedDefaults = toolsetDefaults{
	types.BoolValue(true), types.StringValue("always_allow"), types.BoolValue(true), types.StringValue("always_ask"),
}

// overlay returns d with every value c sets, as Terraform's proposed new state
// does for an Optional+Computed attribute.
func (d toolsetDefaults) overlay(c toolsetDefaults) toolsetDefaults {
	if !c.agentEnabled.IsNull() {
		d.agentEnabled = c.agentEnabled
	}
	if !c.agentPolicy.IsNull() {
		d.agentPolicy = c.agentPolicy
	}
	if !c.mcpEnabled.IsNull() {
		d.mcpEnabled = c.mcpEnabled
	}
	if !c.mcpPolicy.IsNull() {
		d.mcpPolicy = c.mcpPolicy
	}
	return d
}

// toolsetDefaultsAgentJSON is the agent the API returns for
// toolsetDefaultsModel: the built-in toolset and the crm MCP toolset, with the
// given default_config and no per-tool config.
func toolsetDefaultsAgentJSON(version int64, agentEnabled bool, agentPolicy string, mcpEnabled bool, mcpPolicy string) string {
	return fmt.Sprintf(`{"id":"agent_1","name":"n","model":{"id":"claude-sonnet-4-6"},"version":%d,
		"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z",
		"mcp_servers":[{"type":"url","name":"crm","url":"https://mcp.example.com/sse"}],
		"tools":[
			{"type":"agent_toolset_20260401","default_config":{"enabled":%t,"permission_policy":{"type":%q}},"configs":[]},
			{"type":"mcp_toolset","mcp_server_name":"crm","default_config":{"enabled":%t,"permission_policy":{"type":%q}},"configs":[]}]}`,
		version, agentEnabled, agentPolicy, mcpEnabled, mcpPolicy)
}

// toolsetDefaultsModel is the configuration of that agent with the given
// defaults; with a version, it is the state the provider stores for it.
func toolsetDefaultsModel(t *testing.T, d toolsetDefaults, version int64) AgentResourceModel {
	t.Helper()
	m := agentModelFor(parseAgentWithTools(t, `[]`))
	m.MCPServers = types.ListValueMust(types.ObjectType{AttrTypes: agentMCPServerAttrTypes}, []attr.Value{
		types.ObjectValueMust(agentMCPServerAttrTypes, map[string]attr.Value{
			"name": types.StringValue("crm"), "url": types.StringValue("https://mcp.example.com/sse"),
		}),
	})
	m.AgentToolset = types.ObjectValueMust(agentToolsetAttrTypes, map[string]attr.Value{
		"default_enabled":           d.agentEnabled,
		"default_permission_policy": d.agentPolicy,
		"configs":                   types.ListNull(types.ObjectType{AttrTypes: agentToolConfigAttrTypes}),
	})
	m.MCPToolsets = mcpToolsetsList(t, agentMCPToolsetModel{
		MCPServerName:           types.StringValue("crm"),
		DefaultEnabled:          d.mcpEnabled,
		DefaultPermissionPolicy: d.mcpPolicy,
		Configs:                 types.ListNull(types.ObjectType{AttrTypes: agentMCPToolConfigAttrTypes}),
	})
	if version > 0 {
		m.ID = types.StringValue("agent_1")
		m.Version = types.Int64Value(version)
		m.CreatedAt = types.StringValue("2026-01-01T00:00:00Z")
		m.UpdatedAt = types.StringValue("2026-01-01T00:00:00Z")
	}
	return m
}

// plannedDefaults reads the four default_config attributes of a planned state.
func plannedDefaults(t *testing.T, m AgentResourceModel) toolsetDefaults {
	t.Helper()
	var agentToolset agentToolsetModel
	if diags := m.AgentToolset.As(context.Background(), &agentToolset, basetypes.ObjectAsOptions{}); diags.HasError() {
		t.Fatalf("read agent_toolset: %+v", diags)
	}
	mcp := mcpToolsetsFromState(t, m)
	if len(mcp) != 1 {
		t.Fatalf("mcp_toolsets = %+v, want crm", mcp)
	}
	return toolsetDefaults{agentToolset.DefaultEnabled, agentToolset.DefaultPermissionPolicy, mcp[0].DefaultEnabled, mcp[0].DefaultPermissionPolicy}
}

func (d toolsetDefaults) String() string {
	return fmt.Sprintf("agent_toolset{%s, %s} crm{%s, %s}", d.agentEnabled, d.agentPolicy, d.mcpEnabled, d.mcpPolicy)
}

// A toolset default_config attribute the configuration leaves unset plans the
// API's documented default instead of the refreshed value (#499), also inside
// the mcp_toolsets list: the framework applies a schema Default to an
// attribute nested in a list. A state written before the defaults, which holds
// the API's values, plans no change; a value changed outside Terraform plans
// an update back to the default; a configured value is planned as before.
func TestAgentResource_toolsetDefaultsPlan(t *testing.T) {
	t.Parallel()

	p := newAgentProtocol(t, &AgentResource{})
	allowAgent := toolsetDefaults{types.BoolNull(), types.StringNull(), types.BoolNull(), types.StringValue("always_allow")}
	askBuiltIn := toolsetDefaults{types.BoolNull(), types.StringValue("always_ask"), types.BoolNull(), types.StringNull()}
	tests := []struct {
		name   string
		config toolsetDefaults
		prior  toolsetDefaults
		want   toolsetDefaults
	}{
		{
			name:   "unset, state holds the API defaults",
			config: unsetDefaults,
			prior:  documentedDefaults,
			want:   documentedDefaults,
		},
		{
			name:   "unset, MCP policy switched to always_allow out of band",
			config: unsetDefaults,
			prior:  documentedDefaults.overlay(toolsetDefaults{mcpPolicy: types.StringValue("always_allow")}),
			want:   documentedDefaults,
		},
		{
			name:   "unset, every default changed out of band",
			config: unsetDefaults,
			prior: toolsetDefaults{
				types.BoolValue(false), types.StringValue("always_ask"), types.BoolValue(false), types.StringValue("always_allow"),
			},
			want: documentedDefaults,
		},
		{
			name:   "set to a non-default value, unchanged",
			config: allowAgent,
			prior:  documentedDefaults.overlay(allowAgent),
			want:   documentedDefaults.overlay(allowAgent),
		},
		{
			name:   "set to a non-default value, changed out of band",
			config: askBuiltIn,
			prior:  documentedDefaults,
			want:   documentedDefaults.overlay(askBuiltIn),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			prior := p.value(toolsetDefaultsModel(t, tt.prior, 1))
			proposed := p.value(toolsetDefaultsModel(t, tt.prior.overlay(tt.config), 1))
			planned := p.plan(prior, p.value(toolsetDefaultsModel(t, tt.config, 0)), proposed)

			got := plannedDefaults(t, p.model(planned))
			if got.String() != tt.want.String() {
				t.Errorf("planned %s\nwant    %s", got, tt.want)
			}
			if noChange := tt.prior.String() == tt.want.String(); planned.Equal(prior) != noChange {
				t.Errorf("planned state equals prior state: %t, want %t (no change)", planned.Equal(prior), noChange)
			}
		})
	}
}

// The #499 acceptance scenario, against a local server: an agent applied with
// the toolset defaults unset, then switched to always_allow from the Console,
// shows the drift on refresh and an update in the plan; the apply sends the
// documented defaults, and the next refresh and plan are clean.
func TestAgentResource_revertsToolsetDefaultDrift(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	current := toolsetDefaultsAgentJSON(2, true, "always_allow", true, "always_allow") // switched out of band
	var updates [][]byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == http.MethodPost && r.URL.Path == "/v1/agents/agent_1" {
			body, _ := io.ReadAll(r.Body)
			updates = append(updates, body)
			current = toolsetDefaultsAgentJSON(3, true, "always_allow", true, "always_ask")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, current)
	}))
	t.Cleanup(srv.Close)
	p := newAgentProtocol(t, &AgentResource{client: oauthtest.NewSDKClient(t, srv)})

	config := p.value(toolsetDefaultsModel(t, unsetDefaults, 0))
	refreshed := p.read(p.value(toolsetDefaultsModel(t, documentedDefaults, 1)))
	if got := plannedDefaults(t, p.model(refreshed)); got.mcpPolicy.ValueString() != "always_allow" {
		t.Fatalf("refreshed %s, want the out-of-band always_allow on crm", got)
	}

	planned := p.plan(refreshed, config, refreshed)
	if planned.Equal(refreshed) {
		t.Fatal("plan shows no change, want an update reverting crm's policy")
	}
	if got := plannedDefaults(t, p.model(planned)); got.String() != documentedDefaults.String() {
		t.Fatalf("planned %s\nwant    %s", got, documentedDefaults)
	}

	applied := p.apply(refreshed, config, planned)
	if got := plannedDefaults(t, p.model(applied)); got.String() != documentedDefaults.String() {
		t.Errorf("applied %s\nwant    %s", got, documentedDefaults)
	}
	mu.Lock()
	if len(updates) != 1 {
		t.Fatalf("got %d update requests, want 1", len(updates))
	}
	var body struct {
		Version int64 `json:"version"`
		Tools   []struct {
			Type          string `json:"type"`
			DefaultConfig struct {
				Enabled          *bool `json:"enabled"`
				PermissionPolicy struct {
					Type string `json:"type"`
				} `json:"permission_policy"`
			} `json:"default_config"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(updates[0], &body); err != nil {
		t.Fatalf("update body %s: %s", updates[0], err)
	}
	mu.Unlock()
	if body.Version != 2 || len(body.Tools) != 2 {
		t.Fatalf("update body = %+v, want version 2 and both toolsets", body)
	}
	for i, want := range []string{"always_allow", "always_ask"} {
		dc := body.Tools[i].DefaultConfig
		if dc.Enabled == nil || !*dc.Enabled || dc.PermissionPolicy.Type != want {
			t.Errorf("%s default_config = {enabled: %v, permission_policy: %q}, want {true, %q}",
				body.Tools[i].Type, dc.Enabled, dc.PermissionPolicy.Type, want)
		}
	}

	refreshed = p.read(applied)
	if planned := p.plan(refreshed, config, refreshed); !planned.Equal(refreshed) {
		t.Errorf("plan after the apply is not empty: %s", plannedDefaults(t, p.model(planned)))
	}
}

// The per-tool comparison of #395 still holds now that the toolset defaults are
// planned. The configuration enables `search` on crm and leaves its policy and
// crm's default policy unset. An unset per-tool field stays null while the API
// value equals the toolset default the API returns, so an echoed default plans
// no change; a default switched out of band, which `search` inherits, plans an
// update of the toolset default alone; a policy set out of band on `search`
// alone still shows on the tool and plans its removal.
func TestAgentResource_toolConfigsWithPlannedDefaults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		crmPolicy    string // the API's crm default_config.permission_policy
		searchPolicy string // the API's resolved policy for search
		wantDefault  string
		wantSearch   types.String
	}{
		{name: "API echoes the defaults", crmPolicy: "always_ask", searchPolicy: "always_ask",
			wantDefault: "always_ask", wantSearch: types.StringNull()},
		{name: "toolset default switched out of band", crmPolicy: "always_allow", searchPolicy: "always_allow",
			wantDefault: "always_allow", wantSearch: types.StringNull()},
		{name: "per-tool policy set out of band", crmPolicy: "always_ask", searchPolicy: "always_allow",
			wantDefault: "always_ask", wantSearch: types.StringValue("always_allow")},
	}
	withSearch := func(t *testing.T, m AgentResourceModel) AgentResourceModel {
		t.Helper()
		toolsets := mcpToolsetsFromState(t, m)
		toolsets[0].Configs = toolConfigsList(t, agentMCPToolConfigAttrTypes,
			agentToolConfigModel{Name: types.StringValue("search"), Enabled: types.BoolValue(true), PermissionPolicy: types.StringNull()})
		m.MCPToolsets = mcpToolsetsList(t, toolsets...)
		return m
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			agent := strings.Replace(toolsetDefaultsAgentJSON(2, true, "always_allow", true, tt.crmPolicy),
				`"mcp_server_name":"crm","default_config":{"enabled":true,"permission_policy":{"type":"`+tt.crmPolicy+`"}},"configs":[]`,
				`"mcp_server_name":"crm","default_config":{"enabled":true,"permission_policy":{"type":"`+tt.crmPolicy+`"}},`+
					`"configs":[{"name":"search","enabled":true,"permission_policy":{"type":"`+tt.searchPolicy+`"}}]`, 1)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, agent)
			}))
			t.Cleanup(srv.Close)
			p := newAgentProtocol(t, &AgentResource{client: oauthtest.NewSDKClient(t, srv)})

			config := p.value(withSearch(t, toolsetDefaultsModel(t, unsetDefaults, 0)))
			refreshed := p.read(p.value(withSearch(t, toolsetDefaultsModel(t, documentedDefaults, 1))))
			crm := mcpToolsetsFromState(t, p.model(refreshed))[0]
			search := toolConfigsFromList(t, crm.Configs)
			if crm.DefaultPermissionPolicy.ValueString() != tt.wantDefault || len(search) != 1 || !search[0].PermissionPolicy.Equal(tt.wantSearch) {
				t.Fatalf("refreshed crm = %s, configs %+v; want default %s, search policy %s",
					crm.DefaultPermissionPolicy, search, tt.wantDefault, tt.wantSearch)
			}

			// `configs` is not Computed: Terraform proposes the configured list.
			planned := p.plan(refreshed, config, p.value(withSearch(t, p.model(refreshed))))
			wantChange := tt.wantDefault != "always_ask" || !tt.wantSearch.IsNull()
			if planned.Equal(refreshed) == wantChange {
				t.Errorf("plan shows a change: %t, want %t", !planned.Equal(refreshed), wantChange)
			}
			crm = mcpToolsetsFromState(t, p.model(planned))[0]
			search = toolConfigsFromList(t, crm.Configs)
			if crm.DefaultPermissionPolicy.ValueString() != "always_ask" || len(search) != 1 || !search[0].PermissionPolicy.IsNull() {
				t.Errorf("planned crm = %s, configs %+v; want default always_ask, search policy null", crm.DefaultPermissionPolicy, search)
			}
		})
	}
}

// toolsetConfigsAgent is what the server returns for toolsetConfigsPlan: the
// built-in toolset and crm without per-tool configs, billing with an empty
// list, docs with one config.
const toolsetConfigsAgent = `{"id":"agent_1","name":"n","model":{"id":"claude-sonnet-4-6"},"version":2,
	"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-02T00:00:00Z",
	"mcp_servers":[
		{"type":"url","name":"crm","url":"https://crm.example.com/sse"},
		{"type":"url","name":"billing","url":"https://billing.example.com/sse"},
		{"type":"url","name":"docs","url":"https://docs.example.com/sse"}],
	"tools":[
		{"type":"agent_toolset_20260401","default_config":{"enabled":true,"permission_policy":{"type":"always_allow"}},"configs":[]},
		{"type":"mcp_toolset","mcp_server_name":"crm","default_config":{"enabled":true,"permission_policy":{"type":"always_ask"}},"configs":[]},
		{"type":"mcp_toolset","mcp_server_name":"billing","default_config":{"enabled":true,"permission_policy":{"type":"always_ask"}},"configs":[]},
		{"type":"mcp_toolset","mcp_server_name":"docs","default_config":{"enabled":true,"permission_policy":{"type":"always_ask"}},
			"configs":[{"name":"search","enabled":true,"permission_policy":{"type":"always_ask"}}]},
		{"type":"custom","name":"lookup","description":"Look up","input_schema":{"type":"object"}}]}`

// toolsetConfigsPlan plans agent_toolset and crm without `configs`, billing
// with `configs = []` and docs with one config, plus a custom tool.
func toolsetConfigsPlan(t *testing.T) AgentResourceModel {
	t.Helper()
	m := toolsetDefaultsModel(t, documentedDefaults, 0)
	servers := make([]attr.Value, 0, 3)
	for _, name := range []string{"crm", "billing", "docs"} {
		servers = append(servers, types.ObjectValueMust(agentMCPServerAttrTypes, map[string]attr.Value{
			"name": types.StringValue(name), "url": types.StringValue("https://" + name + ".example.com/sse"),
		}))
	}
	m.MCPServers = types.ListValueMust(types.ObjectType{AttrTypes: agentMCPServerAttrTypes}, servers)
	toolset := func(name string, configs types.List) agentMCPToolsetModel {
		return agentMCPToolsetModel{
			MCPServerName:           types.StringValue(name),
			DefaultEnabled:          types.BoolValue(true),
			DefaultPermissionPolicy: types.StringValue("always_ask"),
			Configs:                 configs,
		}
	}
	m.MCPToolsets = mcpToolsetsList(t,
		toolset("crm", types.ListNull(types.ObjectType{AttrTypes: agentMCPToolConfigAttrTypes})),
		toolset("billing", toolConfigsList(t, agentMCPToolConfigAttrTypes)),
		toolset("docs", toolConfigsList(t, agentMCPToolConfigAttrTypes,
			agentToolConfigModel{Name: types.StringValue("search"), Enabled: types.BoolValue(true), PermissionPolicy: types.StringNull()})),
	)
	m.CustomTools = types.ListValueMust(types.ObjectType{AttrTypes: agentCustomToolAttrTypes}, []attr.Value{
		types.ObjectValueMust(agentCustomToolAttrTypes, map[string]attr.Value{
			"name": types.StringValue("lookup"), "description": types.StringValue("Look up"),
			"input_schema": jsontypes.NewNormalizedValue(`{"type":"object"}`),
		}),
	})
	return m
}

// On Update, a toolset planned without per-tool configs (null or empty) sends
// `configs: []` (#508): `tools` is a full replacement, but an omitted `configs`
// might keep the overrides the toolset holds, so one set out of band would
// survive the apply. Planned configs are sent as before, and Create still omits
// the field, since nothing is stored yet.
func TestAgentResource_toolsetConfigsOnUpdate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	var mu sync.Mutex
	bodies := map[string][]byte{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies[r.URL.Path] = body
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, toolsetConfigsAgent)
	}))
	t.Cleanup(srv.Close)
	r := &AgentResource{client: oauthtest.NewSDKClient(t, srv)}
	sch := schematest.ResourceSchema(t, r)

	planned := toolsetConfigsPlan(t)
	plan := tfsdk.Plan{Schema: sch}
	if diags := plan.Set(ctx, &planned); diags.HasError() {
		t.Fatalf("set plan: %+v", diags)
	}
	prior := planned
	prior.ID = types.StringValue("agent_1")
	prior.Version = types.Int64Value(1)
	state := tfsdk.State{Schema: sch}
	if diags := state.Set(ctx, &prior); diags.HasError() {
		t.Fatalf("set state: %+v", diags)
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

	// configsOf returns each tool's raw `configs`, "" when the field is absent.
	configsOf := func(t *testing.T, path string) []string {
		t.Helper()
		mu.Lock()
		raw := bodies[path]
		mu.Unlock()
		var body struct {
			Tools []map[string]json.RawMessage `json:"tools"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			t.Fatalf("%s body %s: %s", path, raw, err)
		}
		got := make([]string, 0, len(body.Tools))
		for _, tool := range body.Tools {
			got = append(got, string(tool["configs"]))
		}
		return got
	}
	const docs = `[{"name":"search","enabled":true}]`
	for _, tt := range []struct {
		name, path string
		want       []string // agent_toolset, crm, billing, docs, custom
	}{
		{"create omits them", "/v1/agents", []string{"", "", "", docs, ""}},
		{"update sends an empty list", "/v1/agents/agent_1", []string{"[]", "[]", "[]", docs, ""}},
	} {
		if got := configsOf(t, tt.path); strings.Join(got, " | ") != strings.Join(tt.want, " | ") {
			t.Errorf("%s: configs = %q\nwant      %q", tt.name, got, tt.want)
		}
	}

	var data AgentResourceModel
	if diags := updateResp.State.Get(ctx, &data); diags.HasError() {
		t.Fatalf("get state: %+v", diags)
	}
	if !data.AgentToolset.Equal(planned.AgentToolset) || !data.MCPToolsets.Equal(planned.MCPToolsets) {
		t.Errorf("state after update = %s, %s\nwant the plan   %s, %s", data.AgentToolset, data.MCPToolsets, planned.AgentToolset, planned.MCPToolsets)
	}
}
