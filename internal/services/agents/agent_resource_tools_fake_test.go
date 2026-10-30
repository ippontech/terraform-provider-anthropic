// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents_test

import (
	"maps"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
)

// null is the knownvalue check of a null attribute.
var null = knownvalue.Null()

func str(s string) knownvalue.Check { return knownvalue.StringExact(s) }

func strSet(values ...string) knownvalue.Check {
	checks := make([]knownvalue.Check, len(values))
	for i, v := range values {
		checks[i] = str(v)
	}
	return knownvalue.SetExact(checks)
}

// builtinConfig is the state value of an agent_toolset.configs entry: name,
// then the attributes set, every other one null.
func builtinConfig(name string, set map[string]knownvalue.Check) knownvalue.Check {
	attrs := map[string]knownvalue.Check{
		"name":               str(name),
		"enabled":            null,
		"permission_policy":  null,
		"allowed_domains":    null,
		"blocked_domains":    null,
		"max_content_tokens": null,
		"user_location":      null,
		"url_sources":        null,
	}
	maps.Copy(attrs, set)
	return knownvalue.ObjectExact(attrs)
}

// urlFilter is the state value of a url_sources tool result filter.
func urlFilter(typ string, tools knownvalue.Check) knownvalue.Check {
	return knownvalue.ObjectExact(map[string]knownvalue.Check{"type": str(typ), "tools": tools})
}

// agentToolset is the state value of agent_toolset.
func agentToolset(defaultEnabled bool, defaultPolicy string, configs ...knownvalue.Check) knownvalue.Check {
	list := knownvalue.Check(null)
	if configs != nil {
		list = knownvalue.ListExact(configs)
	}
	return knownvalue.ObjectExact(map[string]knownvalue.Check{
		"default_enabled":           knownvalue.Bool(defaultEnabled),
		"default_permission_policy": str(defaultPolicy),
		"configs":                   list,
	})
}

func expectAttr(attr string, check knownvalue.Check) statecheck.StateCheck {
	return statecheck.ExpectKnownValue(agentAddr, tfjsonpath.New(attr), check)
}

// The eight built-in tools under every permission policy, enabled set, unset
// and false, with every web option. The create sends exactly what is
// configured, nothing more: an unset enabled or permission_policy is omitted,
// so the tool inherits the toolset default
// (https://platform.claude.com/docs/en/managed-agents/permission-policies).
// The API echoes each config with the inherited value filled in, and state
// keeps the unset ones null while they equal the default. The update swaps
// every policy, so each variant is sent at least once on both create and
// update, and removing the toolset sends an empty tools list.
func TestAgentResource_builtinTools(t *testing.T) {
	t.Parallel()
	srv := newAgentsFake(t)

	const lookupTool = `
  custom_tools = [{
    name         = "lookup"
    description  = "Looks a record up."
    input_schema = jsonencode({ type = "object", properties = { id = { type = "string" } } })
  }]
`
	create := agentConfig(`  agent_toolset = {
    default_enabled           = false
    default_permission_policy = "always_ask"
    configs = [
      { name = "bash", enabled = true, permission_policy = "always_allow" },
      { name = "edit", permission_policy = "always_ask" },
      { name = "read", permission_policy = "auto" },
      { name = "write", enabled = false },
      { name = "glob" },
      { name = "grep", enabled = true, permission_policy = "auto" },
      {
        name               = "web_fetch"
        enabled            = true
        allowed_domains    = ["docs.example.com"]
        max_content_tokens = 4096
        url_sources = {
          user_input          = "none"
          client_tool_results = { type = "only", tools = ["lookup"] }
          server_tool_results = { type = "except", tools = ["web_search"] }
        }
      },
      {
        name            = "web_search"
        blocked_domains = ["ads.example.com"]
        user_location   = { country = "FR", city = "Nantes", region = "Pays de la Loire", timezone = "Europe/Paris" }
      },
    ]
  }
` + lookupTool)
	update := agentConfig(`  agent_toolset = {
    default_permission_policy = "auto"
    configs = [
      { name = "bash", enabled = false, permission_policy = "always_ask" },
      { name = "edit", enabled = true, permission_policy = "auto" },
      { name = "read", enabled = true, permission_policy = "always_allow" },
      { name = "write", enabled = true, permission_policy = "always_ask" },
      { name = "glob", enabled = false, permission_policy = "always_allow" },
      { name = "grep", enabled = false, permission_policy = "always_ask" },
      {
        name              = "web_fetch"
        enabled           = true
        permission_policy = "always_ask"
        blocked_domains   = ["tracker.example.com"]
        url_sources = {
          user_input          = "all"
          client_tool_results = { type = "all" }
          server_tool_results = { type = "none" }
        }
      },
      {
        name              = "web_search"
        enabled           = true
        permission_policy = "auto"
        allowed_domains   = ["example.com/docs", "example.org"]
        user_location     = { country = "US" }
      },
    ]
  }
` + lookupTool)

	const lookupBody = `{"type":"custom","name":"lookup","description":"Looks a record up.",` +
		`"input_schema":{"type":"object","properties":{"id":{"type":"string"}}}}`
	lookupState := knownvalue.ListExact([]knownvalue.Check{knownvalue.ObjectExact(map[string]knownvalue.Check{
		"name":         str("lookup"),
		"description":  str("Looks a record up."),
		"input_schema": str(`{"properties":{"id":{"type":"string"}},"type":"object"}`),
	})})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             checkAgentsArchived(srv),
		Steps: []resource.TestStep{
			{
				Config:           create,
				ConfigPlanChecks: expectAgentAction(plancheck.ResourceActionCreate),
				ConfigStateChecks: []statecheck.StateCheck{
					expectAttr("agent_toolset", agentToolset(false, "always_ask",
						builtinConfig("bash", map[string]knownvalue.Check{"enabled": knownvalue.Bool(true), "permission_policy": str("always_allow")}),
						builtinConfig("edit", map[string]knownvalue.Check{"permission_policy": str("always_ask")}),
						builtinConfig("read", map[string]knownvalue.Check{"permission_policy": str("auto")}),
						builtinConfig("write", map[string]knownvalue.Check{"enabled": knownvalue.Bool(false)}),
						builtinConfig("glob", nil),
						builtinConfig("grep", map[string]knownvalue.Check{"enabled": knownvalue.Bool(true), "permission_policy": str("auto")}),
						builtinConfig("web_fetch", map[string]knownvalue.Check{
							"enabled":            knownvalue.Bool(true),
							"allowed_domains":    strSet("docs.example.com"),
							"max_content_tokens": knownvalue.Int64Exact(4096),
							"url_sources": knownvalue.ObjectExact(map[string]knownvalue.Check{
								"user_input":          str("none"),
								"client_tool_results": urlFilter("only", strSet("lookup")),
								"server_tool_results": urlFilter("except", strSet("web_search")),
							}),
						}),
						builtinConfig("web_search", map[string]knownvalue.Check{
							"blocked_domains": strSet("ads.example.com"),
							"user_location": knownvalue.ObjectExact(map[string]knownvalue.Check{
								"country": str("FR"), "city": str("Nantes"), "region": str("Pays de la Loire"), "timezone": str("Europe/Paris"),
							}),
						}),
					)),
					expectAttr("custom_tools", lookupState),
					expectAttr("mcp_toolsets", null),
					expectAttr("version", knownvalue.Int64Exact(1)),
				},
			},
			// Import maps every API config verbatim: the values state keeps
			// null because they equal the toolset default come back set.
			{
				ResourceName:      agentAddr,
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{
					"agent_toolset.configs.1.enabled", "agent_toolset.configs.2.enabled",
					"agent_toolset.configs.3.permission_policy",
					"agent_toolset.configs.4.enabled", "agent_toolset.configs.4.permission_policy",
					"agent_toolset.configs.6.permission_policy",
					"agent_toolset.configs.7.enabled", "agent_toolset.configs.7.permission_policy",
				},
			},
			{
				Config:           update,
				ConfigPlanChecks: expectAgentAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					expectAttr("agent_toolset", agentToolset(true, "auto",
						builtinConfig("bash", map[string]knownvalue.Check{"enabled": knownvalue.Bool(false), "permission_policy": str("always_ask")}),
						builtinConfig("edit", map[string]knownvalue.Check{"enabled": knownvalue.Bool(true), "permission_policy": str("auto")}),
						builtinConfig("read", map[string]knownvalue.Check{"enabled": knownvalue.Bool(true), "permission_policy": str("always_allow")}),
						builtinConfig("write", map[string]knownvalue.Check{"enabled": knownvalue.Bool(true), "permission_policy": str("always_ask")}),
						builtinConfig("glob", map[string]knownvalue.Check{"enabled": knownvalue.Bool(false), "permission_policy": str("always_allow")}),
						builtinConfig("grep", map[string]knownvalue.Check{"enabled": knownvalue.Bool(false), "permission_policy": str("always_ask")}),
						builtinConfig("web_fetch", map[string]knownvalue.Check{
							"enabled":           knownvalue.Bool(true),
							"permission_policy": str("always_ask"),
							"blocked_domains":   strSet("tracker.example.com"),
							"url_sources": knownvalue.ObjectExact(map[string]knownvalue.Check{
								"user_input":          str("all"),
								"client_tool_results": urlFilter("all", null),
								"server_tool_results": urlFilter("none", null),
							}),
						}),
						builtinConfig("web_search", map[string]knownvalue.Check{
							"enabled":           knownvalue.Bool(true),
							"permission_policy": str("auto"),
							"allowed_domains":   strSet("example.com/docs", "example.org"),
							"user_location": knownvalue.ObjectExact(map[string]knownvalue.Check{
								"country": str("US"), "city": null, "region": null, "timezone": null,
							}),
						}),
					)),
					expectAttr("version", knownvalue.Int64Exact(2)),
				},
			},
			// Every per-tool value is configured: the import matches state.
			{
				ResourceName:      agentAddr,
				ImportState:       true,
				ImportStateVerify: true,
			},
			// A toolset without configs: only its default is sent, the API
			// resolves the policy.
			{
				Config:           agentConfig("  agent_toolset = { default_enabled = true }\n" + lookupTool),
				ConfigPlanChecks: expectAgentAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					expectAttr("agent_toolset", agentToolset(true, "always_allow")),
				},
			},
			{
				Config:           agentConfig(lookupTool),
				ConfigPlanChecks: expectAgentAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					expectAttr("agent_toolset", null),
					expectAttr("custom_tools", lookupState),
				},
			},
		},
	})

	srv.AssertJSONBodies(routeCreateAgent, `{"name":"test-agent","model":{"id":"claude-sonnet-4-6"},"tools":[
		{"type":"agent_toolset_20260401",
		 "default_config":{"enabled":false,"permission_policy":{"type":"always_ask"}},
		 "configs":[
			{"name":"bash","enabled":true,"permission_policy":{"type":"always_allow"}},
			{"name":"edit","permission_policy":{"type":"always_ask"}},
			{"name":"read","permission_policy":{"type":"auto"}},
			{"name":"write","enabled":false},
			{"name":"glob"},
			{"name":"grep","enabled":true,"permission_policy":{"type":"auto"}},
			{"name":"web_fetch","enabled":true,"allowed_domains":["docs.example.com"],"max_content_tokens":4096,
			 "url_sources":{"user_input":{"type":"none"},
				"client_tool_results":{"type":"only","tools":[{"type":"tool_reference","name":"lookup"}]},
				"server_tool_results":{"type":"except","tools":[{"type":"tool_reference","name":"web_search"}]}}},
			{"name":"web_search","blocked_domains":["ads.example.com"],
			 "user_location":{"type":"approximate","country":"FR","city":"Nantes","region":"Pays de la Loire","timezone":"Europe/Paris"}}]},
		`+lookupBody+`]}`)
	srv.AssertJSONBodies(routeUpdateAgent,
		`{"name":"test-agent","description":"","system":"","version":1,"model":{"id":"claude-sonnet-4-6","effort":"high"},
		  "mcp_servers":[],"skills":[],"tools":[
			{"type":"agent_toolset_20260401",
			 "default_config":{"permission_policy":{"type":"auto"}},
			 "configs":[
				{"name":"bash","enabled":false,"permission_policy":{"type":"always_ask"}},
				{"name":"edit","enabled":true,"permission_policy":{"type":"auto"}},
				{"name":"read","enabled":true,"permission_policy":{"type":"always_allow"}},
				{"name":"write","enabled":true,"permission_policy":{"type":"always_ask"}},
				{"name":"glob","enabled":false,"permission_policy":{"type":"always_allow"}},
				{"name":"grep","enabled":false,"permission_policy":{"type":"always_ask"}},
				{"name":"web_fetch","enabled":true,"permission_policy":{"type":"always_ask"},"blocked_domains":["tracker.example.com"],
				 "url_sources":{"user_input":{"type":"all"},"client_tool_results":{"type":"all"},"server_tool_results":{"type":"none"}}},
				{"name":"web_search","enabled":true,"permission_policy":{"type":"auto"},"allowed_domains":["example.com/docs","example.org"],
				 "user_location":{"type":"approximate","country":"US"}}]},
			`+lookupBody+`]}`,
		`{"name":"test-agent","description":"","system":"","version":2,"model":{"id":"claude-sonnet-4-6","effort":"high"},
		  "mcp_servers":[],"skills":[],"tools":[{"type":"agent_toolset_20260401","default_config":{"enabled":true}},`+lookupBody+`]}`,
		`{"name":"test-agent","description":"","system":"","version":3,"model":{"id":"claude-sonnet-4-6","effort":"high"},
		  "mcp_servers":[],"skills":[],"tools":[`+lookupBody+`]}`)
	srv.AssertCalled(routeArchiveAgent, 1)
	srv.assertRequestShape(t)
}
