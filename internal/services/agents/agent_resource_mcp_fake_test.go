// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
)

// mcpConfig is the state value of an mcp_toolsets[*].configs entry.
func mcpConfig(name string, enabled, policy knownvalue.Check) knownvalue.Check {
	return knownvalue.ObjectExact(map[string]knownvalue.Check{"name": str(name), "enabled": enabled, "permission_policy": policy})
}

// mcpToolset is the state value of an mcp_toolsets entry.
func mcpToolset(server string, defaultEnabled bool, defaultPolicy string, configs ...knownvalue.Check) knownvalue.Check {
	list := knownvalue.Check(null)
	if configs != nil {
		list = knownvalue.ListExact(configs)
	}
	return knownvalue.ObjectExact(map[string]knownvalue.Check{
		"mcp_server_name":           str(server),
		"default_enabled":           knownvalue.Bool(defaultEnabled),
		"default_permission_policy": str(defaultPolicy),
		"configs":                   list,
	})
}

func mcpServer(name, url string) knownvalue.Check {
	return knownvalue.ObjectExact(map[string]knownvalue.Check{"name": str(name), "url": str(url)})
}

// mutateAgent changes the stored agent id behind Terraform's back.
func (f *agentsFake) mutateAgent(t *testing.T, id string, change func(obj map[string]any)) {
	t.Helper()
	obj, ok := f.agents.Get(id)
	if !ok {
		t.Fatalf("%s is not in the fake", id)
	}
	change(obj)
	f.agents.Put(obj)
}

// MCP servers and toolsets: a toolset left to the API defaults (enabled,
// always_ask), one with every default and per-tool policy set, then an
// update that removes a server, reorders the configs and switches the
// default policy to auto. A per-tool override relaxed out of band, set out
// of band on a tool that inherits the default, or added out of band shows up
// as drift and is reverted by the next apply. Removing both attributes sends
// empty lists.
func TestAgentResource_mcpToolsets(t *testing.T) {
	t.Parallel()
	srv := newAgentsFake(t)

	create := agentConfig(`  mcp_servers = [
    { name = "docs", url = "https://docs.example.com/mcp" },
    { name = "crm", url = "https://crm.example.com/mcp" },
  ]
  mcp_toolsets = [
    { mcp_server_name = "docs" },
    {
      mcp_server_name           = "crm"
      default_enabled           = false
      default_permission_policy = "always_allow"
      configs = [
        { name = "search", enabled = true, permission_policy = "always_ask" },
        { name = "update", permission_policy = "auto" },
        { name = "delete", enabled = false },
        { name = "read" },
      ]
    },
  ]
`)
	update := agentConfig(`  mcp_servers = [{ name = "crm", url = "https://crm.example.com/mcp" }]
  mcp_toolsets = [{
    mcp_server_name           = "crm"
    default_permission_policy = "auto"
    configs = [
      { name = "read", enabled = true, permission_policy = "always_allow" },
      { name = "search", enabled = false, permission_policy = "always_ask" },
      { name = "audit" },
    ]
  }]
`)
	updated := []statecheck.StateCheck{
		expectAttr("mcp_servers", knownvalue.ListExact([]knownvalue.Check{mcpServer("crm", "https://crm.example.com/mcp")})),
		expectAttr("mcp_toolsets", knownvalue.ListExact([]knownvalue.Check{
			mcpToolset("crm", true, "auto",
				mcpConfig("read", knownvalue.Bool(true), str("always_allow")),
				mcpConfig("search", knownvalue.Bool(false), str("always_ask")),
				mcpConfig("audit", null, null)),
		})),
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             checkAgentsArchived(srv),
		Steps: []resource.TestStep{
			{
				Config:           create,
				ConfigPlanChecks: expectAgentAction(plancheck.ResourceActionCreate),
				ConfigStateChecks: []statecheck.StateCheck{
					expectAttr("mcp_servers", knownvalue.ListExact([]knownvalue.Check{
						mcpServer("docs", "https://docs.example.com/mcp"),
						mcpServer("crm", "https://crm.example.com/mcp"),
					})),
					expectAttr("mcp_toolsets", knownvalue.ListExact([]knownvalue.Check{
						mcpToolset("docs", true, "always_ask"),
						mcpToolset("crm", false, "always_allow",
							mcpConfig("search", knownvalue.Bool(true), str("always_ask")),
							mcpConfig("update", null, str("auto")),
							mcpConfig("delete", knownvalue.Bool(false), null),
							mcpConfig("read", null, null)),
					})),
					expectAttr("agent_toolset", null),
					expectAttr("custom_tools", null),
				},
			},
			// Import maps every API config verbatim, inherited values included.
			{
				ResourceName:      agentAddr,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{
					"mcp_toolsets.1.configs.1.enabled",
					"mcp_toolsets.1.configs.2.permission_policy",
					"mcp_toolsets.1.configs.3.enabled", "mcp_toolsets.1.configs.3.permission_policy",
				},
			},
			{
				Config:            update,
				ConfigPlanChecks:  expectAgentAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: updated,
			},
			// Out of band: search relaxed to always_allow, audit (which
			// inherits auto) set to always_allow, and a delete config added
			// with always_allow. All three show up, so the plan reverts them.
			{
				PreConfig: func() {
					srv.mutateAgent(t, agentID(1), func(obj map[string]any) {
						toolset := objects(obj["tools"])[0]
						configs := toolset["configs"].([]any)
						configs[1].(map[string]any)["permission_policy"] = map[string]any{"type": "always_allow"}
						configs[2].(map[string]any)["permission_policy"] = map[string]any{"type": "always_allow"}
						toolset["configs"] = append(configs, map[string]any{
							"name": "delete", "enabled": true, "permission_policy": map[string]any{"type": "always_allow"},
						})
					})
				},
				Config:            update,
				ConfigPlanChecks:  expectAgentAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: updated,
			},
			// Out of band, alone: audit, whose policy is not configured,
			// set to always_allow instead of the auto it inherits. It still
			// shows up, and the next apply reverts it.
			{
				PreConfig: func() {
					srv.mutateAgent(t, agentID(1), func(obj map[string]any) {
						configs := objects(obj["tools"])[0]["configs"].([]any)
						configs[2].(map[string]any)["permission_policy"] = map[string]any{"type": "always_allow"}
					})
				},
				Config:            update,
				ConfigPlanChecks:  expectAgentAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: updated,
			},
			{
				ResourceName:            agentAddr,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"mcp_toolsets.0.configs.2.enabled", "mcp_toolsets.0.configs.2.permission_policy"},
			},
			{
				Config:           agentConfig(""),
				ConfigPlanChecks: expectAgentAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					expectAttr("mcp_servers", null),
					expectAttr("mcp_toolsets", null),
				},
			},
		},
	})

	srv.AssertJSONBodies(routeCreateAgent, `{"name":"test-agent","model":{"id":"claude-sonnet-4-6"},
		"mcp_servers":[{"name":"docs","type":"url","url":"https://docs.example.com/mcp"},{"name":"crm","type":"url","url":"https://crm.example.com/mcp"}],
		"tools":[
			{"type":"mcp_toolset","mcp_server_name":"docs"},
			{"type":"mcp_toolset","mcp_server_name":"crm",
			 "default_config":{"enabled":false,"permission_policy":{"type":"always_allow"}},
			 "configs":[
				{"name":"search","enabled":true,"permission_policy":{"type":"always_ask"}},
				{"name":"update","permission_policy":{"type":"auto"}},
				{"name":"delete","enabled":false},
				{"name":"read"}]}]}`)
	const updateTools = `"mcp_servers":[{"name":"crm","type":"url","url":"https://crm.example.com/mcp"}],"skills":[],
		"tools":[{"type":"mcp_toolset","mcp_server_name":"crm","default_config":{"permission_policy":{"type":"auto"}},
			"configs":[{"name":"read","enabled":true,"permission_policy":{"type":"always_allow"}},
				{"name":"search","enabled":false,"permission_policy":{"type":"always_ask"}},
				{"name":"audit"}]}]}`
	srv.AssertJSONBodies(routeUpdateAgent,
		`{"name":"test-agent","description":"","system":"","version":1,"model":{"id":"claude-sonnet-4-6","effort":"high"},`+updateTools,
		`{"name":"test-agent","description":"","system":"","version":2,"model":{"id":"claude-sonnet-4-6","effort":"high"},`+updateTools,
		`{"name":"test-agent","description":"","system":"","version":3,"model":{"id":"claude-sonnet-4-6","effort":"high"},`+updateTools,
		`{"name":"test-agent","description":"","system":"","version":4,"model":{"id":"claude-sonnet-4-6","effort":"high"},
		  "mcp_servers":[],"skills":[],"tools":[]}`)
	srv.assertRequestShape(t)
}
