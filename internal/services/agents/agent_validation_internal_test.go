// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// renderDiags renders diagnostics as "severity|path|summary|detail" lines, in
// order, so a test pins every field of every diagnostic.
func renderDiags(diags diag.Diagnostics) []string {
	lines := make([]string, len(diags))
	for i, d := range diags {
		p := ""
		if withPath, ok := d.(interface{ Path() path.Path }); ok {
			p = withPath.Path().String()
		}
		lines[i] = fmt.Sprintf("%s|%s|%s|%s", d.Severity(), p, d.Summary(), d.Detail())
	}
	return lines
}

func requireDiags(t *testing.T, got diag.Diagnostics, want ...string) {
	t.Helper()
	lines := renderDiags(got)
	if strings.Join(lines, "\n") != strings.Join(want, "\n") {
		t.Errorf("diagnostics:\n  %s\nwant:\n  %s", strings.Join(lines, "\n  "), strings.Join(want, "\n  "))
	}
}

// Every rule of the coordinator roster in one configuration, pinned with
// the order of the diagnostics: per entry in list order, then the counts.
// An unknown entry and an entry of unknown type are skipped.
func TestValidateMultiagentConfig_rosterDiagnostics(t *testing.T) {
	t.Parallel()
	str, null := types.StringValue, types.StringNull()
	nv, v := types.Int64Null(), types.Int64Value
	unknownType := types.ObjectValueMust(agentMultiagentEntryAttrTypes, map[string]attr.Value{
		"type": types.StringUnknown(), "id": str("z"), "version": v(1), "model": str("m"),
	})

	diags := validateMultiagentConfig(context.Background(), multiagentObj(t,
		multiagentEntryObj(t, "agent", str("a"), nv, null),
		multiagentEntryObj(t, "agent", str("a"), v(2), null),
		multiagentEntryObj(t, "agent", null, nv, str("m")),
		multiagentEntryObj(t, "self", str("s"), v(1), str("m")),
		multiagentEntryObj(t, "self", null, nv, null),
		multiagentEntryObj(t, "advisor", str("x"), v(2), null),
		multiagentEntryObj(t, "advisor", null, nv, str("m")),
		types.ObjectUnknown(agentMultiagentEntryAttrTypes),
		unknownType,
	))
	requireDiags(t, diags,
		`Error|multiagent.agents|Duplicate agent entry|Roster entries must reference distinct agents; "a" appears more than once.`,
		`Error|multiagent.agents[2].id|Missing roster agent ID|"id" is required when type is "agent".`,
		`Error|multiagent.agents[2].model|Unexpected roster model|"model" is only allowed when type is "advisor".`,
		`Error|multiagent.agents[3].id|Unexpected roster agent ID|"id" is only allowed when type is "agent".`,
		`Error|multiagent.agents[3].model|Unexpected roster model|"model" is only allowed when type is "advisor".`,
		`Error|multiagent.agents[3].version|Unexpected roster version|"version" is only allowed when type is "agent".`,
		`Error|multiagent.agents[5].model|Missing advisor model|"model" is required when type is "advisor".`,
		`Error|multiagent.agents[5].id|Unexpected roster agent ID|"id" is only allowed when type is "agent".`,
		`Error|multiagent.agents[5].version|Unexpected roster version|"version" is only allowed when type is "agent".`,
		`Error|multiagent.agents|Duplicate self entry|At most one roster entry may have type "self".`,
		`Error|multiagent.agents|Duplicate advisor entry|At most one roster entry may have type "advisor".`,
	)
}

// The settings each type rejects, and the member rules, pinned in full.
func TestValidateMultiagentConfig_typeAndMemberDiagnostics(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	id := types.StringValue
	agentA := predefinedObj(t, "agent", id("a"), int64Null)
	self := predefinedObj(t, "self", strNull, int64Null)

	t.Run("coordinator without roster, with the other type's settings", func(t *testing.T) {
		t.Parallel()
		attrs := multiagent20261001Obj(t, memberObj(t, boolNull, boolNull), memberObj(t, boolNull, boolNull), "m").Attributes()
		attrs["type"] = types.StringValue("coordinator")
		requireDiags(t, validateMultiagentConfig(ctx, types.ObjectValueMust(agentMultiagentAttrTypes, attrs)),
			`Error|multiagent.agents|Missing roster|"agents" is required when type is "coordinator".`,
			`Error|multiagent.subagents|Unexpected multiagent setting|"subagents" is only allowed when type is "multiagent_20261001".`,
			`Error|multiagent.workflows|Unexpected multiagent setting|"workflows" is only allowed when type is "multiagent_20261001".`,
			`Error|multiagent.advisor|Unexpected multiagent setting|"advisor" is only allowed when type is "multiagent_20261001".`,
		)
	})

	t.Run("multiagent_20261001 with a roster", func(t *testing.T) {
		t.Parallel()
		attrs := multiagent20261001Obj(t, noMember, noMember, "").Attributes()
		attrs["agents"] = multiagentObj(t, multiagentEntryObj(t, "self", strNull, int64Null, strNull), multiagentEntryObj(t, "self", strNull, int64Null, strNull)).Attributes()["agents"]
		requireDiags(t, validateMultiagentConfig(ctx, types.ObjectValueMust(agentMultiagentAttrTypes, attrs)),
			`Error|multiagent.agents|Unexpected roster|"agents" is only allowed when type is "coordinator": list the agents in "subagents" or "workflows" instead.`,
		)
	})

	t.Run("members", func(t *testing.T) {
		t.Parallel()
		unknownEntry := types.ObjectUnknown(agentMultiagentPredefinedAgentAttrTypes)
		requireDiags(t, validateMultiagentConfig(ctx, multiagent20261001Obj(t,
			memberObj(t, types.BoolValue(false), types.BoolValue(true), agentA),
			memberObj(t, boolNull, types.BoolValue(false),
				predefinedObj(t, "agent", strNull, int64Null),
				agentA, agentA,
				predefinedObj(t, "self", id("s"), types.Int64Value(1)),
				self,
				predefinedObj(t, "agent", types.StringUnknown(), int64Null),
				unknownEntry,
			), "")),
			`Error|multiagent.subagents.inline_agents_enabled|Unexpected setting on a disabled member|"inline_agents_enabled" is only allowed when "enabled" is true.`,
			`Error|multiagent.subagents.predefined_agents|Unexpected setting on a disabled member|"predefined_agents" is only allowed when "enabled" is true.`,
			`Error|multiagent.workflows.predefined_agents[0].id|Missing predefined agent ID|"id" is required when type is "agent".`,
			`Error|multiagent.workflows.predefined_agents|Duplicate predefined agent|Predefined agents must be distinct; "a" appears more than once.`,
			`Error|multiagent.workflows.predefined_agents[3].id|Unexpected predefined agent ID|"id" is only allowed when type is "agent".`,
			`Error|multiagent.workflows.predefined_agents[3].version|Unexpected predefined agent version|"version" is only allowed when type is "agent".`,
			`Error|multiagent.workflows.predefined_agents|Duplicate self entry|At most one predefined agent may have type "self".`,
		)
	})

	t.Run("inline agents off without predefined agents", func(t *testing.T) {
		t.Parallel()
		requireDiags(t, validateMultiagentConfig(ctx, multiagent20261001Obj(t, memberObj(t, types.BoolValue(true), types.BoolValue(false)), noMember, "")),
			`Error|multiagent.subagents.predefined_agents|Missing predefined agents|"predefined_agents" must list at least one agent when "inline_agents_enabled" is false.`,
		)
	})

	t.Run("disabled with unknown settings", func(t *testing.T) {
		t.Parallel()
		member := types.ObjectValueMust(agentMultiagentMemberAttrTypes, map[string]attr.Value{
			"enabled":               types.BoolValue(false),
			"inline_agents_enabled": types.BoolUnknown(),
			"predefined_agents":     types.ListUnknown(types.ObjectType{AttrTypes: agentMultiagentPredefinedAgentAttrTypes}),
		})
		requireDiags(t, validateMultiagentConfig(ctx, multiagent20261001Obj(t, member, types.ObjectUnknown(agentMultiagentMemberAttrTypes), "")))
	})
}

// Every web option rule on one toolset, pinned with the order of the
// diagnostics: per entry, the domain conflict, the location, the URL
// sources, then the options the tool does not take.
func TestValidateAgentToolConfigs_diagnostics(t *testing.T) {
	t.Parallel()
	with := func(name string, f func(*agentToolConfigModel)) attr.Value {
		c := toolCfg(name)
		f(&c)
		return toolCfgObj(t, c)
	}
	us := userLocation(types.StringValue("US"), types.StringNull(), types.StringNull(), types.StringNull())
	emptyLocation := userLocation(types.StringNull(), types.StringNull(), types.StringNull(), types.StringNull())

	diags := validateAgentToolConfigs(context.Background(), agentToolsetObj(t,
		with("web_fetch", func(c *agentToolConfigModel) {
			c.AllowedDomains = strSet("a.example.com")
			c.BlockedDomains = strSet("b.example.com")
			c.UserLocation = emptyLocation
			c.URLSources = urlSources(types.StringNull(), noURLFilter, noURLFilter)
		}),
		with("bash", func(c *agentToolConfigModel) {
			c.AllowedDomains = strSet("a.example.com")
			c.MaxContentTokens = types.Int64Value(10)
			c.UserLocation = us
			c.URLSources = urlSources(types.StringValue("all"), noURLFilter, noURLFilter)
		}),
		with("web_fetch", func(c *agentToolConfigModel) {
			c.URLSources = urlSources(types.StringValue("none"), urlFilter("none"), urlFilter("none"))
		}),
		with("web_fetch", func(c *agentToolConfigModel) {
			c.URLSources = urlSources(types.StringNull(), urlFilter("only"), urlFilter("all", "web_search"))
		}),
		with("web_search", func(c *agentToolConfigModel) {
			c.Name = types.StringUnknown()
			c.MaxContentTokens = types.Int64Value(10)
		}),
		types.ObjectUnknown(agentToolConfigAttrTypes),
		with("web_search", func(c *agentToolConfigModel) {
			c.BlockedDomains = strSet("b.example.com")
			c.MaxContentTokens = types.Int64Value(10)
		}),
	))
	requireDiags(t, diags,
		`Error|agent_toolset.configs[0].blocked_domains|Conflicting domain lists|Set either "allowed_domains" or "blocked_domains" on a tool, not both.`,
		`Error|agent_toolset.configs[0].user_location|Empty user location|"user_location" must set at least one of "country", "city", "region" or "timezone".`,
		`Error|agent_toolset.configs[0].url_sources|Empty URL sources|"url_sources" must set at least one of "user_input", "client_tool_results" or "server_tool_results"; omit it to allow every source.`,
		`Error|agent_toolset.configs[0].user_location|Unsupported tool option|"user_location" is only allowed when name is "web_search", not "web_fetch".`,
		`Error|agent_toolset.configs[1].allowed_domains|Unsupported tool option|"allowed_domains" is only allowed when name is "web_fetch" or "web_search", not "bash".`,
		`Error|agent_toolset.configs[1].max_content_tokens|Unsupported tool option|"max_content_tokens" is only allowed when name is "web_fetch", not "bash".`,
		`Error|agent_toolset.configs[1].user_location|Unsupported tool option|"user_location" is only allowed when name is "web_search", not "bash".`,
		`Error|agent_toolset.configs[1].url_sources|Unsupported tool option|"url_sources" is only allowed when name is "web_fetch", not "bash".`,
		`Error|agent_toolset.configs[2].url_sources|No URL source left|Setting "user_input", "client_tool_results" and "server_tool_results" all to "none" is rejected by the API: disable web_fetch instead (enabled = false).`,
		`Error|agent_toolset.configs[3].url_sources.client_tool_results.tools|Missing tools|"tools" is required when "type" is "only".`,
		`Error|agent_toolset.configs[3].url_sources.server_tool_results.tools|Unexpected tools|"tools" is only allowed when "type" is "only" or "except", not "all".`,
		`Error|agent_toolset.configs[6].max_content_tokens|Unsupported tool option|"max_content_tokens" is only allowed when name is "web_fetch", not "web_search".`,
	)
}

// An unknown entry, which may still resolve to a valid one, is skipped, and
// the entries after it are still checked, in the roster and in a
// predefined_agents list.
func TestValidateMultiagentConfig_entriesAfterAnUnknownOne(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	requireDiags(t, validateMultiagentConfig(ctx, multiagentObj(t,
		types.ObjectUnknown(agentMultiagentEntryAttrTypes),
		multiagentEntryObj(t, "agent", types.StringNull(), types.Int64Null(), types.StringNull()),
	)), `Error|multiagent.agents[1].id|Missing roster agent ID|"id" is required when type is "agent".`)
	requireDiags(t, validateMultiagentConfig(ctx, multiagent20261001Obj(t,
		memberObj(t, boolNull, boolNull,
			types.ObjectUnknown(agentMultiagentPredefinedAgentAttrTypes),
			predefinedObj(t, "agent", strNull, int64Null)),
		noMember, "")),
		`Error|multiagent.subagents.predefined_agents[1].id|Missing predefined agent ID|"id" is required when type is "agent".`)
}
