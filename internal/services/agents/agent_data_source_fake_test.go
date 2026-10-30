// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents_test

import (
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// seedFullAgent stores an agent with every attribute set, outside Terraform,
// as the first agent of the fake. Its multiagent roster lists the agent
// itself, which the API resolves into a reference to its own ID.
func seedFullAgent(srv *agentsFake) {
	srv.seedAgent(map[string]any{
		"name":        "full",
		"description": "Answers questions.",
		"system":      "You are terse.",
		"model":       map[string]any{"id": "claude-opus-4-6", "speed": "fast", "effort": "max", "inference_geo": "eu"},
		"metadata":    map[string]any{"team": "a"},
		"mcp_servers": []any{map[string]any{"name": "crm", "type": "url", "url": "https://crm.example.com/mcp"}},
		"skills":      []any{map[string]any{"type": "anthropic", "skill_id": "xlsx", "version": "3"}},
		"tools": []any{
			map[string]any{"type": "agent_toolset_20260401", "configs": []any{
				map[string]any{"name": "web_fetch", "allowed_domains": []any{"docs.example.com"}, "url_sources": map[string]any{"user_input": "none"}},
				map[string]any{"name": "bash", "enabled": false},
			}},
			map[string]any{"type": "mcp_toolset", "mcp_server_name": "crm", "configs": []any{
				map[string]any{"name": "search", "permission_policy": map[string]any{"type": "auto"}},
			}},
			map[string]any{"type": "custom", "name": "lookup", "description": "Looks a record up.", "input_schema": map[string]any{"type": "object"}},
		},
		"multiagent": map[string]any{"type": "coordinator", "agents": []any{
			map[string]any{"type": "self"},
			map[string]any{"type": "advisor", "model": "claude-opus-4-6"},
		}},
		"execution_identity": map[string]any{"type": "aws_role", "role_arn": "arn:aws:iam::123456789012:role/agent-runner"},
	})
}

// fullAgent is the data source value of the agent seedFullAgent stores: every
// API field verbatim, inherited tool values and the resolved roster included.
func fullAgent() map[string]knownvalue.Check {
	return map[string]knownvalue.Check{
		"id":                  str(agentID(1)),
		"name":                str("full"),
		"model":               str("claude-opus-4-6"),
		"model_speed":         str("fast"),
		"model_effort":        str("max"),
		"model_inference_geo": str("eu"),
		"description":         str("Answers questions."),
		"system":              str("You are terse."),
		"metadata":            knownvalue.MapExact(map[string]knownvalue.Check{"team": str("a")}),
		"mcp_servers":         knownvalue.ListExact([]knownvalue.Check{mcpServer("crm", "https://crm.example.com/mcp")}),
		"skills":              knownvalue.ListExact([]knownvalue.Check{skill("anthropic", "xlsx", "3")}),
		"agent_toolset": agentToolset(true, "always_allow",
			builtinConfig("web_fetch", map[string]knownvalue.Check{
				"enabled":           knownvalue.Bool(true),
				"permission_policy": str("always_allow"),
				"allowed_domains":   strSet("docs.example.com"),
				"url_sources": knownvalue.ObjectExact(map[string]knownvalue.Check{
					"user_input": str("none"), "client_tool_results": null, "server_tool_results": null,
				}),
			}),
			builtinConfig("bash", map[string]knownvalue.Check{"enabled": knownvalue.Bool(false), "permission_policy": str("always_allow")}),
		),
		"mcp_toolsets": knownvalue.ListExact([]knownvalue.Check{
			mcpToolset("crm", true, "always_ask", mcpConfig("search", knownvalue.Bool(true), str("auto"))),
		}),
		"custom_tools": knownvalue.ListExact([]knownvalue.Check{customTool("lookup", "Looks a record up.", str(`{"type":"object"}`))}),
		"multiagent": knownvalue.ObjectExact(map[string]knownvalue.Check{
			"type": str("coordinator"),
			"agents": knownvalue.ListExact([]knownvalue.Check{
				rosterEntry("agent", str(agentID(1)), version(1), null),
				rosterEntry("advisor", null, null, str("claude-opus-4-6")),
			}),
			"subagents": null,
			"workflows": null,
			"advisor":   null,
		}),
		"execution_identity": knownvalue.ObjectExact(map[string]knownvalue.Check{
			"type": str("aws_role"), "role_arn": str("arn:aws:iam::123456789012:role/agent-runner"),
		}),
		"version":     version(1),
		"created_at":  str("2026-01-01T00:00:01Z"),
		"updated_at":  str("2026-01-01T00:00:01Z"),
		"archived_at": null,
	}
}

// bareAgent is the data source value of an agent seeded with a name only,
// as the n-th agent of the fake, created and last updated at second created
// and updated: the API defaults (an effort, the service account) and nulls.
func bareAgent(n int, name, created, updated, archived string) map[string]knownvalue.Check {
	archivedAt := knownvalue.Check(null)
	if archived != "" {
		archivedAt = str(archived)
	}
	return map[string]knownvalue.Check{
		"id":                  str(agentID(n)),
		"name":                str(name),
		"model":               str("claude-sonnet-4-6"),
		"model_speed":         null,
		"model_effort":        str("high"),
		"model_inference_geo": null,
		"description":         null,
		"system":              null,
		"metadata":            null,
		"mcp_servers":         null,
		"skills":              null,
		"agent_toolset":       null,
		"mcp_toolsets":        null,
		"custom_tools":        null,
		"multiagent":          null,
		"execution_identity":  knownvalue.ObjectExact(map[string]knownvalue.Check{"type": str("service_account"), "role_arn": null}),
		"version":             version(1),
		"created_at":          str(created),
		"updated_at":          str(updated),
		"archived_at":         archivedAt,
	}
}

// expectDataSource checks every attribute of a data source.
func expectDataSource(addr string, attrs map[string]knownvalue.Check) []statecheck.StateCheck {
	var checks []statecheck.StateCheck
	for name, check := range attrs {
		checks = append(checks, statecheck.ExpectKnownValue(addr, tfjsonpath.New(name), check))
	}
	return checks
}

// TestAgentDataSource_read reads an agent with every attribute and one with
// none: every value comes from the response as is.
func TestAgentDataSource_read(t *testing.T) {
	t.Parallel()
	srv := newAgentsFake(t)
	seedFullAgent(srv)
	srv.seedAgent(map[string]any{"name": "bare"})

	full := fullAgent()
	full["agent_id"] = str(agentID(1))
	bare := bareAgent(2, "bare", "2026-01-01T00:00:02Z", "2026-01-01T00:00:02Z", "")
	bare["agent_id"] = str(agentID(2))

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.APIKey) + `
data "anthropic_agent" "full" {
  agent_id = "` + agentID(1) + `"
}

data "anthropic_agent" "bare" {
  agent_id = "` + agentID(2) + `"
}
`,
			ConfigStateChecks: append(expectDataSource("data.anthropic_agent.full", full), expectDataSource("data.anthropic_agent.bare", bare)...),
		}},
	})
	srv.AssertCalled(routeListAgents, 0)
	srv.assertRequestShape(t)
}

// TestAgentDataSource_notFound: an unknown agent is an error diagnostic.
func TestAgentDataSource_notFound(t *testing.T) {
	t.Parallel()
	srv := newAgentsFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.APIKey) + `
data "anthropic_agent" "test" {
  agent_id = "agent_missing"
}
`,
			ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+retrieve\s+agent:.*404\s+Not\s+Found`),
		}},
	})
	srv.AssertCalled(routeGetAgent, 1)
}

// seedListed stores the full agent and four bare ones, and archives the
// third: created at seconds 1 to 5, the archive at second 6.
func seedListed(t *testing.T, srv *agentsFake) {
	t.Helper()
	seedFullAgent(srv)
	for _, name := range []string{"bare-2", "bare-3", "bare-4", "bare-5"} {
		srv.seedAgent(map[string]any{"name": name})
	}
	if _, ok := srv.agents.Archive(agentID(3)); !ok {
		t.Fatalf("%s is not in the fake", agentID(3))
	}
}

func listOf(agents ...map[string]knownvalue.Check) knownvalue.Check {
	checks := make([]knownvalue.Check, len(agents))
	for i, a := range agents {
		checks[i] = knownvalue.ObjectExact(a)
	}
	return knownvalue.ListExact(checks)
}

// TestAgentsDataSource_pages lists the agents two per page (limit = 2): the
// data source follows next_page with page= and keeps the API order. The
// archived agent is left out unless include_archived is true, which takes
// a third page.
func TestAgentsDataSource_pages(t *testing.T) {
	t.Parallel()
	srv := newAgentsFake(t)
	seedListed(t, srv)
	const archivedAt = "2026-01-01T00:00:06Z"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.APIKey) + `
data "anthropic_agents" "test" {
  limit = 2
}
`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.anthropic_agents.test", tfjsonpath.New("agents"), listOf(
					fullAgent(),
					bareAgent(2, "bare-2", "2026-01-01T00:00:02Z", "2026-01-01T00:00:02Z", ""),
					bareAgent(4, "bare-4", "2026-01-01T00:00:04Z", "2026-01-01T00:00:04Z", ""),
					bareAgent(5, "bare-5", "2026-01-01T00:00:05Z", "2026-01-01T00:00:05Z", ""),
				)),
				statecheck.ExpectKnownValue("data.anthropic_agents.test", tfjsonpath.New("include_archived"), null),
				statecheck.ExpectKnownValue("data.anthropic_agents.test", tfjsonpath.New("limit"), knownvalue.Int64Exact(2)),
			},
		}},
	})
	srv.AssertWalks(routeListAgents,
		"beta=true&limit=2",
		"beta=true&limit=2&page="+sdktest.NextPageToken(agentID(2)))
	srv.AssertAllPagesRead(routeListAgents)
	srv.assertRequestShape(t)

	archived := newAgentsFake(t)
	seedListed(t, archived)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(archived.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.APIKey) + `
data "anthropic_agents" "test" {
  include_archived = true
  limit            = 2
}
`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.anthropic_agents.test", tfjsonpath.New("agents"), listOf(
					fullAgent(),
					bareAgent(2, "bare-2", "2026-01-01T00:00:02Z", "2026-01-01T00:00:02Z", ""),
					bareAgent(3, "bare-3", "2026-01-01T00:00:03Z", archivedAt, archivedAt),
					bareAgent(4, "bare-4", "2026-01-01T00:00:04Z", "2026-01-01T00:00:04Z", ""),
					bareAgent(5, "bare-5", "2026-01-01T00:00:05Z", "2026-01-01T00:00:05Z", ""),
				)),
			},
		}},
	})
	archived.AssertWalks(routeListAgents,
		"beta=true&include_archived=true&limit=2",
		"beta=true&include_archived=true&limit=2&page="+sdktest.NextPageToken(agentID(2)),
		"beta=true&include_archived=true&limit=2&page="+sdktest.NextPageToken(agentID(4)))
	archived.AssertAllPagesRead(routeListAgents)
}

// TestAgentsDataSource_createdAtBounds sends both bounds as RFC 3339 query
// parameters and gets the agents created within them, bounds included; the
// API's default page size is left alone.
func TestAgentsDataSource_createdAtBounds(t *testing.T) {
	t.Parallel()
	srv := newAgentsFake(t)
	seedListed(t, srv)
	srv.listPageSize = 1

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.APIKey) + `
data "anthropic_agents" "test" {
  created_at_gte   = "2026-01-01T00:00:02Z"
  created_at_lte   = "2026-01-01T00:00:04Z"
  include_archived = false
}
`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.anthropic_agents.test", tfjsonpath.New("agents"), listOf(
					bareAgent(2, "bare-2", "2026-01-01T00:00:02Z", "2026-01-01T00:00:02Z", ""),
					bareAgent(4, "bare-4", "2026-01-01T00:00:04Z", "2026-01-01T00:00:04Z", ""),
				)),
			},
		}},
	})
	const bounds = "beta=true&created_at%5Bgte%5D=2026-01-01T00%3A00%3A02Z&created_at%5Blte%5D=2026-01-01T00%3A00%3A04Z&include_archived=false"
	srv.AssertWalks(routeListAgents, bounds, bounds+"&page="+sdktest.NextPageToken(agentID(2)))
	srv.AssertAllPagesRead(routeListAgents)
}

// TestAgentsDataSource_empty: no agent is an empty list, never null.
func TestAgentsDataSource_empty(t *testing.T) {
	t.Parallel()
	srv := newAgentsFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.APIKey) + `
data "anthropic_agents" "test" {}
`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.anthropic_agents.test", tfjsonpath.New("agents"), knownvalue.ListExact([]knownvalue.Check{})),
			},
		}},
	})
	srv.AssertWalks(routeListAgents, "beta=true")
}

// TestAgentsDataSource_errors: a timestamp that is not RFC 3339 fails before
// any request, naming the attribute; an API error (here the 400 for a limit
// above 100) is an error diagnostic.
func TestAgentsDataSource_errors(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		attrs string
		want  *regexp.Regexp
		calls int
	}{
		"created_at_gte": {`created_at_gte = "yesterday"`, regexp.MustCompile(`(?s)Invalid\s+RFC\s+3339\s+timestamp.*created_at_gte\s+"yesterday"`), 0},
		"created_at_lte": {`created_at_lte = "2026-01-01"`, regexp.MustCompile(`(?s)Invalid\s+RFC\s+3339\s+timestamp.*created_at_lte\s+"2026-01-01"`), 0},
		"api error":      {`limit = 101`, regexp.MustCompile(`(?s)Unable\s+to\s+list\s+agents:.*400\s+Bad\s+Request`), 1},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newAgentsFake(t)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
				Steps: []resource.TestStep{{
					Config:      providertest.ProviderConfig(sdktest.APIKey) + "\ndata \"anthropic_agents\" \"test\" {\n  " + tc.attrs + "\n}\n",
					ExpectError: tc.want,
				}},
			})
			srv.AssertCalled(routeListAgents, tc.calls)
		})
	}
}

// TestAgentsDataSource_listErrorStops: an error on a later page fails the
// read; the pages already read are discarded.
func TestAgentsDataSource_listErrorStops(t *testing.T) {
	t.Parallel()
	srv := newAgentsFake(t)
	seedListed(t, srv)
	srv.listPageSize = 2
	srv.pageFault = http.StatusForbidden

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config:      providertest.ProviderConfig(sdktest.APIKey) + "\ndata \"anthropic_agents\" \"test\" {}\n",
			ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+list\s+agents:.*403\s+Forbidden`),
		}},
	})
	srv.AssertCalled(routeListAgents, 2)
}
