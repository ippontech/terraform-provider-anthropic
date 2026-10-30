// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
)

// rosterEntry is the state value of a coordinator roster entry.
func rosterEntry(typ string, id, version, model knownvalue.Check) knownvalue.Check {
	return knownvalue.ObjectExact(map[string]knownvalue.Check{"type": str(typ), "id": id, "version": version, "model": model})
}

// predefinedAgent is the state value of a predefined_agents entry.
func predefinedAgent(typ string, id, version knownvalue.Check) knownvalue.Check {
	return knownvalue.ObjectExact(map[string]knownvalue.Check{"type": str(typ), "id": id, "version": version})
}

func multiagentMember(enabled, inline, predefined knownvalue.Check) knownvalue.Check {
	return knownvalue.ObjectExact(map[string]knownvalue.Check{"enabled": enabled, "inline_agents_enabled": inline, "predefined_agents": predefined})
}

func version(n int64) knownvalue.Check { return knownvalue.Int64Exact(n) }

// seedHelpers stores two agents created outside Terraform, at versions 4 and
// 2, for the multiagent configurations to reference. The agent under test
// is then the third the fake creates.
func seedHelpers(srv *agentsFake) (helper1, helper2 string) {
	srv.seedAgent(map[string]any{"name": "helper-1", "version": 4})
	srv.seedAgent(map[string]any{"name": "helper-2", "version": 2})
	return agentID(1), agentID(2)
}

// importedAttr checks one attribute of the imported resource against its
// JSON encoding in the flatmap Terraform keeps (key "attr.0.field").
func importedAttr(want map[string]string) resource.ImportStateCheckFunc {
	return func(states []*terraform.InstanceState) error {
		if len(states) != 1 {
			return fmt.Errorf("imported %d instances, want 1", len(states))
		}
		for k, v := range want {
			if got := states[0].Attributes[k]; got != v {
				return fmt.Errorf("imported %s = %q, want %q", k, got, v)
			}
		}
		return nil
	}
}

// The coordinator roster with the three entry kinds, in an order the API
// does not keep: it resolves self into a reference to the agent itself,
// pins an unversioned reference to the latest version and echoes the advisor
// last. State keeps the configured order and kinds, with the pinned
// versions. An unrelated update re-sends the pinned version, even after the
// referenced agent moved on; import maps the API roster verbatim; removing
// the block sends multiagent: null.
func TestAgentResource_multiagentCoordinator(t *testing.T) {
	t.Parallel()
	srv := newAgentsFake(t)
	helper1, helper2 := seedHelpers(srv)
	owner := agentID(3)

	roster := fmt.Sprintf(`  multiagent = {
    type = "coordinator"
    agents = [
      { type = "advisor", model = "claude-opus-4-6" },
      { type = "agent", id = %q },
      { type = "self" },
      { type = "agent", id = %q, version = 1 },
    ]
  }
`, helper1, helper2)
	expectRoster := expectAttr("multiagent", knownvalue.ObjectExact(map[string]knownvalue.Check{
		"type": str("coordinator"),
		"agents": knownvalue.ListExact([]knownvalue.Check{
			rosterEntry("advisor", null, null, str("claude-opus-4-6")),
			rosterEntry("agent", str(helper1), version(4), null),
			rosterEntry("self", null, null, null),
			rosterEntry("agent", str(helper2), version(1), null),
		}),
		"subagents": null,
		"workflows": null,
		"advisor":   null,
	}))

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             checkArchived(srv, owner),
		Steps: []resource.TestStep{
			{
				Config:            agentConfig(roster),
				ConfigPlanChecks:  expectAgentAction(plancheck.ResourceActionCreate),
				ConfigStateChecks: []statecheck.StateCheck{expectRoster, expectAttr("id", str(owner))},
			},
			// Without a configuration to correlate with, import keeps the API
			// order: self as a reference to the agent itself, the advisor last.
			{
				ResourceName:            agentAddr,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"multiagent.agents"},
				ImportStateCheck: importedAttr(map[string]string{
					"multiagent.agents.#":         "4",
					"multiagent.agents.0.type":    "agent",
					"multiagent.agents.0.id":      helper1,
					"multiagent.agents.0.version": "4",
					"multiagent.agents.1.type":    "agent",
					"multiagent.agents.1.id":      owner,
					"multiagent.agents.1.version": "1",
					"multiagent.agents.2.id":      helper2,
					"multiagent.agents.2.version": "1",
					"multiagent.agents.3.type":    "advisor",
					"multiagent.agents.3.model":   "claude-opus-4-6",
				}),
			},
			// helper-1 moves to version 5: the update keeps the pinned 4.
			{
				PreConfig: func() {
					srv.mutateAgent(t, helper1, func(obj map[string]any) { obj["version"] = json.Number("5") })
				},
				Config:            agentConfig(`  description = "coordinates"` + "\n" + roster),
				ConfigPlanChecks:  expectAgentAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{expectRoster, expectAttr("version", version(2))},
			},
			{
				Config:            agentConfig(""),
				ConfigPlanChecks:  expectAgentAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{expectAttr("multiagent", null)},
			},
		},
	})

	sentRoster := func(helper1Version string) string {
		return `{"type":"coordinator","agents":[{"type":"advisor","model":"claude-opus-4-6"},` +
			`{"type":"agent","id":"` + helper1 + `"` + helper1Version + `},{"type":"self"},` +
			`{"type":"agent","id":"` + helper2 + `","version":1}]}`
	}
	srv.AssertJSONBodies(routeCreateAgent, `{"name":"test-agent","model":{"id":"claude-sonnet-4-6"},"multiagent":`+sentRoster("")+`}`)
	srv.AssertJSONBodies(routeUpdateAgent,
		`{"name":"test-agent","description":"coordinates","system":"","version":1,"model":{"id":"claude-sonnet-4-6","effort":"high"},
		  "mcp_servers":[],"skills":[],"tools":[],"multiagent":`+sentRoster(`,"version":4`)+`}`,
		`{"name":"test-agent","description":"","system":"","version":2,"model":{"id":"claude-sonnet-4-6","effort":"high"},
		  "mcp_servers":[],"skills":[],"tools":[],"multiagent":null}`)
	srv.assertRequestShape(t)
}

// multiagent_20261001: every member is sent with every key, defaults
// included, so the API's level-by-level merge always ends on the plan.
// Subagents restricted to predefined agents (one referencing the agent
// itself), workflows disabled and an advisor; an unrelated update that keeps
// the pinned version of a predefined agent; then subagents left to their
// defaults (null in state), workflows with one pinned agent and no advisor;
// subagents then set to its defaults (the same body, kept as configured);
// then a switch to the coordinator type, which replaces the configuration.
func TestAgentResource_multiagent20261001(t *testing.T) {
	t.Parallel()
	srv := newAgentsFake(t)
	helper1, helper2 := seedHelpers(srv)
	owner := agentID(3)

	create := fmt.Sprintf(`  multiagent = {
    type = "multiagent_20261001"
    subagents = {
      inline_agents_enabled = false
      predefined_agents     = [{ type = "agent", id = %q }, { type = "self" }]
    }
    workflows = { enabled = false }
    advisor   = { model = "claude-opus-4-6" }
  }
`, helper1)
	update := fmt.Sprintf(`  multiagent = {
    type      = "multiagent_20261001"
    workflows = { predefined_agents = [{ type = "agent", id = %q, version = 2 }] }
  }
`, helper2)
	coordinator := fmt.Sprintf(`  multiagent = {
    type   = "coordinator"
    agents = [{ type = "agent", id = %q }]
  }
`, helper2)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             checkArchived(srv, owner),
		Steps: []resource.TestStep{
			{
				Config:           agentConfig(create),
				ConfigPlanChecks: expectAgentAction(plancheck.ResourceActionCreate),
				ConfigStateChecks: []statecheck.StateCheck{expectAttr("multiagent", knownvalue.ObjectExact(map[string]knownvalue.Check{
					"type":   str("multiagent_20261001"),
					"agents": null,
					"subagents": multiagentMember(null, knownvalue.Bool(false), knownvalue.ListExact([]knownvalue.Check{
						predefinedAgent("agent", str(helper1), version(4)),
						predefinedAgent("self", null, null),
					})),
					"workflows": multiagentMember(knownvalue.Bool(false), null, null),
					"advisor":   knownvalue.ObjectExact(map[string]knownvalue.Check{"model": str("claude-opus-4-6")}),
				}))},
			},
			// Without a configuration to correlate with, import reads self as
			// a reference to the agent itself; enabled, an API default, stays
			// null.
			{
				ResourceName:            agentAddr,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"multiagent.subagents"},
				ImportStateCheck: importedAttr(map[string]string{
					"multiagent.subagents.enabled":                     "",
					"multiagent.subagents.inline_agents_enabled":       "false",
					"multiagent.subagents.predefined_agents.#":         "2",
					"multiagent.subagents.predefined_agents.0.id":      helper1,
					"multiagent.subagents.predefined_agents.1.type":    "agent",
					"multiagent.subagents.predefined_agents.1.id":      owner,
					"multiagent.subagents.predefined_agents.1.version": "1",
				}),
			},
			// helper-1 moves to version 5: an unrelated update re-sends the
			// pinned 4.
			{
				PreConfig: func() {
					srv.mutateAgent(t, helper1, func(obj map[string]any) { obj["version"] = json.Number("5") })
				},
				Config:           agentConfig(`  description = "pinned"` + "\n" + create),
				ConfigPlanChecks: expectAgentAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{expectAttr("multiagent", knownvalue.ObjectExact(map[string]knownvalue.Check{
					"type":   str("multiagent_20261001"),
					"agents": null,
					"subagents": multiagentMember(null, knownvalue.Bool(false), knownvalue.ListExact([]knownvalue.Check{
						predefinedAgent("agent", str(helper1), version(4)),
						predefinedAgent("self", null, null),
					})),
					"workflows": multiagentMember(knownvalue.Bool(false), null, null),
					"advisor":   knownvalue.ObjectExact(map[string]knownvalue.Check{"model": str("claude-opus-4-6")}),
				}))},
			},
			{
				Config:           agentConfig(update),
				ConfigPlanChecks: expectAgentAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{expectAttr("multiagent", knownvalue.ObjectExact(map[string]knownvalue.Check{
					"type":      str("multiagent_20261001"),
					"agents":    null,
					"subagents": null,
					"workflows": multiagentMember(null, null, knownvalue.ListExact([]knownvalue.Check{
						predefinedAgent("agent", str(helper2), version(2)),
					})),
					"advisor": null,
				}))},
			},
			// subagents set to its defaults: the same body, and state keeps
			// the configured attribute, the other two null.
			{
				Config:           agentConfig(strings.Replace(update, "type      = \"multiagent_20261001\"\n", "type      = \"multiagent_20261001\"\n    subagents = { inline_agents_enabled = true }\n", 1)),
				ConfigPlanChecks: expectAgentAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{expectAttr("multiagent", knownvalue.ObjectExact(map[string]knownvalue.Check{
					"type":      str("multiagent_20261001"),
					"agents":    null,
					"subagents": multiagentMember(null, knownvalue.Bool(true), null),
					"workflows": multiagentMember(null, null, knownvalue.ListExact([]knownvalue.Check{
						predefinedAgent("agent", str(helper2), version(2)),
					})),
					"advisor": null,
				}))},
			},
			{
				Config:           agentConfig(coordinator),
				ConfigPlanChecks: expectAgentAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{expectAttr("multiagent", knownvalue.ObjectExact(map[string]knownvalue.Check{
					"type":      str("coordinator"),
					"agents":    knownvalue.ListExact([]knownvalue.Check{rosterEntry("agent", str(helper2), version(2), null)}),
					"subagents": null,
					"workflows": null,
					"advisor":   null,
				}))},
			},
		},
	})

	srv.AssertJSONBodies(routeCreateAgent, `{"name":"test-agent","model":{"id":"claude-sonnet-4-6"},"multiagent":{"type":"multiagent_20261001",
		"subagents":{"type":"enabled","inline_agents":{"type":"disabled"},"predefined_agents":[{"type":"agent","id":"`+helper1+`"},{"type":"self"}]},
		"workflows":{"type":"disabled"},
		"advisor":{"type":"enabled","model":"claude-opus-4-6"}}}`)
	srv.AssertJSONBodies(routeUpdateAgent,
		`{"name":"test-agent","description":"pinned","system":"","version":1,"model":{"id":"claude-sonnet-4-6","effort":"high"},
		  "mcp_servers":[],"skills":[],"tools":[],"multiagent":{"type":"multiagent_20261001",
			"subagents":{"type":"enabled","inline_agents":{"type":"disabled"},"predefined_agents":[{"type":"agent","id":"`+helper1+`","version":4},{"type":"self"}]},
			"workflows":{"type":"disabled"},
			"advisor":{"type":"enabled","model":"claude-opus-4-6"}}}`,
		`{"name":"test-agent","description":"","system":"","version":2,"model":{"id":"claude-sonnet-4-6","effort":"high"},
		  "mcp_servers":[],"skills":[],"tools":[],"multiagent":{"type":"multiagent_20261001",
			"subagents":{"type":"enabled","inline_agents":{"type":"enabled"},"predefined_agents":[]},
			"workflows":{"type":"enabled","inline_agents":{"type":"enabled"},"predefined_agents":[{"type":"agent","id":"`+helper2+`","version":2}]},
			"advisor":{"type":"disabled"}}}`,
		`{"name":"test-agent","description":"","system":"","version":3,"model":{"id":"claude-sonnet-4-6","effort":"high"},
		  "mcp_servers":[],"skills":[],"tools":[],"multiagent":{"type":"multiagent_20261001",
			"subagents":{"type":"enabled","inline_agents":{"type":"enabled"},"predefined_agents":[]},
			"workflows":{"type":"enabled","inline_agents":{"type":"enabled"},"predefined_agents":[{"type":"agent","id":"`+helper2+`","version":2}]},
			"advisor":{"type":"disabled"}}}`,
		`{"name":"test-agent","description":"","system":"","version":4,"model":{"id":"claude-sonnet-4-6","effort":"high"},
		  "mcp_servers":[],"skills":[],"tools":[],"multiagent":{"type":"coordinator","agents":[{"type":"agent","id":"`+helper2+`"}]}}`)
	srv.assertRequestShape(t)
}
