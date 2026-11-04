// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
)

// Lists configured empty stay empty in state: the API returns [] for an
// agent without MCP servers, skills or tools ("mcp_servers: array",
// "skills: array", "tools: array", https://platform.claude.com/docs/en/api/beta/agents/create,
// "Returns"), whether they were never set or set to [], and the provider
// used to map that to null, which failed the apply with "Provider produced
// inconsistent result after apply: .skills: was [], but now null".
func TestAgentResource_emptyLists(t *testing.T) {
	t.Parallel()
	srv := newAgentsFake(t)

	const empty = "  mcp_servers  = []\n  skills       = []\n  mcp_toolsets = []\n  custom_tools = []\n"
	emptyLists := []statecheck.StateCheck{}
	for _, attr := range []string{"mcp_servers", "skills", "mcp_toolsets", "custom_tools"} {
		emptyLists = append(emptyLists, statecheck.ExpectKnownValue(agentAddr, tfjsonpath.New(attr), knownvalue.ListExact([]knownvalue.Check{})))
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             checkAgentsArchived(srv),
		Steps: []resource.TestStep{
			{
				Config:            agentConfig(empty),
				ConfigPlanChecks:  expectAgentAction(plancheck.ResourceActionCreate),
				ConfigStateChecks: emptyLists,
			},
			// Import has no configuration to keep: the lists read back null.
			{
				ResourceName:            agentAddr,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"mcp_servers", "skills", "mcp_toolsets", "custom_tools"},
			},
			{
				Config:            agentConfig(empty + "  description = \"changed\"\n"),
				ConfigPlanChecks:  expectAgentAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: emptyLists,
			},
		},
	})

	srv.AssertJSONBodies(routeCreateAgent, `{"name":"test-agent","model":{"id":"claude-sonnet-4-6"},"mcp_servers":[]}`)
	srv.AssertJSONBodies(routeUpdateAgent, `{"name":"test-agent","description":"changed","system":"","version":1,`+
		`"model":{"id":"claude-sonnet-4-6"},"mcp_servers":[],"skills":[],"tools":[]}`)
	srv.assertRequestShape(t)
}
