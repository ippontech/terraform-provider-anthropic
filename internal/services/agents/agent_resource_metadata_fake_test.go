// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents_test

import (
	"fmt"
	"reflect"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
)

// agentUpdateBody is the body the provider sends to update the agent of
// agentConfig at version, with metadataPatch and extra JSON members. The
// effort is the default the API resolved on create, which the plan keeps
// while the model is unchanged.
func agentUpdateBody(version int, metadataPatch, extra string) string {
	return fmt.Sprintf(`{"name":"test-agent","description":"","system":"","version":%d,`+
		`"model":{"id":"claude-sonnet-4-6","effort":"high"},"mcp_servers":[],"skills":[],"tools":[],"metadata":%s%s}`,
		version, metadataPatch, extra)
}

// expectMetadata checks the agent's metadata in state: a map, or null.
func expectMetadata(metadata map[string]string) []statecheck.StateCheck {
	var check knownvalue.Check = knownvalue.Null()
	if metadata != nil {
		values := map[string]knownvalue.Check{}
		for k, v := range metadata {
			values[k] = knownvalue.StringExact(v)
		}
		check = knownvalue.MapExact(values)
	}
	return []statecheck.StateCheck{statecheck.ExpectKnownValue(agentAddr, tfjsonpath.New("metadata"), check)}
}

// expectAgentAction checks the action planned for the agent before the
// apply, and an empty plan after the refresh that follows it.
func expectAgentAction(action plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return providertest.ExpectAction(agentAddr, action)
}

// expectStoredMetadata checks the metadata of the one agent the fake holds.
func expectStoredMetadata(srv *agentsFake, want map[string]any) resource.TestCheckFunc {
	return func(*terraform.State) error {
		agents := srv.agents.List(nil)
		if len(agents) != 1 {
			return fmt.Errorf("the fake holds %d agents, want 1", len(agents))
		}
		if got := agents[0]["metadata"]; !reflect.DeepEqual(got, want) {
			return fmt.Errorf("stored metadata = %#v, want %#v", got, want)
		}
		return nil
	}
}

// Removing one metadata key deletes it server-side: the update sends it as
// null, since the API keeps omitted keys. `metadata = {}` then deletes the
// rest and stays an empty map in state.
func TestAgentResource_metadataKeyRemoved(t *testing.T) {
	t.Parallel()
	srv := newAgentsFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             checkAgentsArchived(srv),
		Steps: []resource.TestStep{
			{
				Config:            agentConfig("  metadata = { team = \"a\", env = \"dev\" }\n"),
				ConfigPlanChecks:  expectAgentAction(plancheck.ResourceActionCreate),
				ConfigStateChecks: expectMetadata(map[string]string{"team": "a", "env": "dev"}),
			},
			{
				Config:            agentConfig("  metadata = { team = \"a\" }\n"),
				ConfigPlanChecks:  expectAgentAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectMetadata(map[string]string{"team": "a"}),
				Check:             expectStoredMetadata(srv, map[string]any{"team": "a"}),
			},
			{
				Config:            agentConfig("  metadata = {}\n"),
				ConfigPlanChecks:  expectAgentAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectMetadata(map[string]string{}),
				Check:             expectStoredMetadata(srv, map[string]any{}),
			},
		},
	})

	srv.AssertCalled(routeCreateAgent, 1)
	srv.AssertJSONBodies(routeUpdateAgent,
		agentUpdateBody(1, `{"team":"a","env":null}`, ""),
		agentUpdateBody(2, `{"team":null}`, ""))
	srv.assertRequestShape(t)
}

// Removing the metadata attribute deletes every key: the update used to send
// {}, which the API treats as "no change".
func TestAgentResource_metadataRemoved(t *testing.T) {
	t.Parallel()
	srv := newAgentsFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             checkAgentsArchived(srv),
		Steps: []resource.TestStep{
			{
				Config:            agentConfig("  metadata = { team = \"a\", env = \"dev\" }\n"),
				ConfigPlanChecks:  expectAgentAction(plancheck.ResourceActionCreate),
				ConfigStateChecks: expectMetadata(map[string]string{"team": "a", "env": "dev"}),
			},
			{
				Config:            agentConfig(""),
				ConfigPlanChecks:  expectAgentAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectMetadata(nil),
				Check:             expectStoredMetadata(srv, map[string]any{}),
			},
		},
	})

	srv.AssertJSONBodies(routeUpdateAgent, agentUpdateBody(1, `{"team":null,"env":null}`, ""))
	srv.assertRequestShape(t)
}

// Clearing multiagent and removing a metadata key in the same update sends
// both: the two travel as extra fields, set in one SetExtraFields call
// because a second call would replace the first.
func TestAgentResource_metadataAndMultiagentClearedTogether(t *testing.T) {
	t.Parallel()
	srv := newAgentsFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             checkAgentsArchived(srv),
		Steps: []resource.TestStep{
			{
				Config: agentConfig(`  metadata   = { team = "a", env = "dev" }
  multiagent = {
    type   = "coordinator"
    agents = [{ type = "advisor", model = "claude-opus-5-5" }]
  }
`),
				ConfigPlanChecks: expectAgentAction(plancheck.ResourceActionCreate),
			},
			{
				Config:           agentConfig("  metadata = { team = \"a\" }\n"),
				ConfigPlanChecks: expectAgentAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: append(expectMetadata(map[string]string{"team": "a"}),
					statecheck.ExpectKnownValue(agentAddr, tfjsonpath.New("multiagent"), knownvalue.Null())),
				Check: expectStoredMetadata(srv, map[string]any{"team": "a"}),
			},
		},
	})

	srv.AssertJSONBodies(routeUpdateAgent, agentUpdateBody(1, `{"team":"a","env":null}`, `,"multiagent":null`))
	srv.assertRequestShape(t)
}
