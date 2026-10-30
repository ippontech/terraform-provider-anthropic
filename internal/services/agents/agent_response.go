// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents

import (
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
)

// Response-to-value helpers shared by the resource and the data sources. An
// absent or empty API field maps to null.

// agentTools is the API's tools list split by kind.
type agentTools struct {
	toolset *anthropic.BetaManagedAgentsAgentToolset20260401
	mcp     []anthropic.BetaManagedAgentsMCPToolset
	custom  []anthropic.BetaManagedAgentsCustomTool
}

// splitAgentTools splits the API's tools into the agent toolset (the last one
// wins), the MCP toolsets and the custom tools; other kinds are ignored.
func splitAgentTools(tools []anthropic.BetaManagedAgentsAgentToolUnion) agentTools {
	var split agentTools
	for _, tool := range tools {
		switch tool.Type {
		case "agent_toolset_20260401":
			t := tool.AsAgentToolset20260401()
			split.toolset = &t
		case "mcp_toolset":
			split.mcp = append(split.mcp, tool.AsMCPToolset())
		case "custom":
			split.custom = append(split.custom, tool.AsCustom())
		}
	}
	return split
}

// keepNull returns current instead of v when both are null, so a value the
// API leaves absent keeps the null the plan or prior state held.
func keepNull[T interface{ IsNull() bool }](v, current T) T {
	if v.IsNull() && current.IsNull() {
		return current
	}
	return v
}

func formatTime(t time.Time) types.String {
	return types.StringValue(t.Format(time.RFC3339))
}

// metadataValue maps the API metadata, null when empty.
func metadataValue(metadata map[string]string) (types.Map, diag.Diagnostics) {
	if len(metadata) == 0 {
		return types.MapNull(types.StringType), nil
	}
	elements := make(map[string]attr.Value, len(metadata))
	for k, v := range metadata {
		elements[k] = types.StringValue(v)
	}
	return types.MapValue(types.StringType, elements)
}

// mcpServersValue maps the API MCP servers, null when there are none.
func mcpServersValue(servers []anthropic.BetaManagedAgentsMCPServerURLDefinition) (types.List, diag.Diagnostics) {
	elemType := types.ObjectType{AttrTypes: agentMCPServerAttrTypes}
	if len(servers) == 0 {
		return types.ListNull(elemType), nil
	}
	var diags diag.Diagnostics
	objs := make([]attr.Value, len(servers))
	for i, s := range servers {
		obj, d := types.ObjectValue(agentMCPServerAttrTypes, map[string]attr.Value{
			"name": types.StringValue(s.Name),
			"url":  types.StringValue(s.URL),
		})
		diags.Append(d...)
		objs[i] = obj
	}
	list, d := types.ListValue(elemType, objs)
	diags.Append(d...)
	return list, diags
}

// skillsValue maps the API skills, with their resolved versions, null when
// there are none.
func skillsValue(skills []anthropic.BetaManagedAgentsAgentSkillUnion) (types.List, diag.Diagnostics) {
	elemType := types.ObjectType{AttrTypes: agentSkillAttrTypes}
	if len(skills) == 0 {
		return types.ListNull(elemType), nil
	}
	var diags diag.Diagnostics
	objs := make([]attr.Value, len(skills))
	for i, s := range skills {
		obj, d := types.ObjectValue(agentSkillAttrTypes, map[string]attr.Value{
			"type":     types.StringValue(s.Type),
			"skill_id": types.StringValue(s.SkillID),
			"version":  types.StringValue(s.Version),
		})
		diags.Append(d...)
		objs[i] = obj
	}
	list, d := types.ListValue(elemType, objs)
	diags.Append(d...)
	return list, diags
}

// customToolsValue maps the API custom tools, null when there are none.
// inputSchema maps each tool's raw input_schema JSON: rawInputSchema for the
// data sources, the configured schema kept for the resource
// (customToolInputSchemaState).
func customToolsValue(tools []anthropic.BetaManagedAgentsCustomTool, inputSchema func(name, raw string) jsontypes.Normalized) (types.List, diag.Diagnostics) {
	elemType := types.ObjectType{AttrTypes: agentCustomToolAttrTypes}
	if len(tools) == 0 {
		return types.ListNull(elemType), nil
	}
	var diags diag.Diagnostics
	objs := make([]attr.Value, len(tools))
	for i, t := range tools {
		obj, d := types.ObjectValue(agentCustomToolAttrTypes, map[string]attr.Value{
			"name":         types.StringValue(t.Name),
			"description":  types.StringValue(t.Description),
			"input_schema": inputSchema(t.Name, t.InputSchema.RawJSON()),
		})
		diags.Append(d...)
		objs[i] = obj
	}
	list, d := types.ListValue(elemType, objs)
	diags.Append(d...)
	return list, diags
}

// rawInputSchema keeps the API's raw input_schema JSON as a normalized
// value, null when absent.
func rawInputSchema(_, raw string) jsontypes.Normalized {
	if raw == "" || raw == "null" {
		return jsontypes.NewNormalizedNull()
	}
	return jsontypes.NewNormalizedValue(raw)
}

// modelSpeedAfterResponse is the state value of model_speed. The API returns
// "" for the standard speed: a null or unknown current value becomes null,
// and a configured one (e.g. "standard") is kept, so it causes no diff.
func modelSpeedAfterResponse(speed anthropic.BetaManagedAgentsModelConfigSpeed, current types.String) types.String {
	if speed != "" {
		return types.StringValue(string(speed))
	}
	if !isKnown(current) {
		return types.StringNull()
	}
	return current
}

// stringAfterResponse is the state value of an optional string the API
// returns as "" when unset: null then, the current null kept as is.
func stringAfterResponse(s string, current types.String) types.String {
	return keepNull(tfvalue.StringOrNull(s), current)
}
