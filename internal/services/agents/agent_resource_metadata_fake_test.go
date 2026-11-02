// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents_test

import (
	"fmt"
	"reflect"
	"strings"
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
// agentConfig at version, with metadataPatch and extra JSON members.
func agentUpdateBody(version int, metadataPatch, extra string) string {
	return fmt.Sprintf(`{"name":"test-agent","description":"","system":"","version":%d,`+
		`"model":{"id":"claude-sonnet-4-6"},"mcp_servers":[],"skills":[],"tools":[],"metadata":%s%s}`,
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
	return resource.ConfigPlanChecks{
		PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(agentAddr, action)},
		PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
	}
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
// null, since the API keeps omitted keys, and leaves the unchanged key out.
// `metadata = {}` then deletes the rest and stays an empty map in state.
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
		agentUpdateBody(1, `{"env":null}`, ""),
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

	srv.AssertJSONBodies(routeUpdateAgent, agentUpdateBody(1, `{"env":null}`, `,"multiagent":null`))
	srv.assertRequestShape(t)
}

// sixteenKeys is a metadata attribute with the 16 pairs the schema allows,
// k1 = "v1" to k16 = "v16", the last key named last.
func sixteenKeys(last string) (string, map[string]string) {
	values := map[string]string{}
	var pairs []string
	for i := 1; i <= 16; i++ {
		k := fmt.Sprintf("k%d", i)
		if i == 16 {
			k = last
		}
		values[k] = fmt.Sprintf("v%d", i)
		pairs = append(pairs, fmt.Sprintf("%s = %q", k, values[k]))
	}
	return "  metadata = { " + strings.Join(pairs, ", ") + " }\n", values
}

// The update sends only the metadata that changes: renaming one key of a
// full map sends the new key and a null for the old one, two entries rather
// than the 16 planned keys plus the null, and an update that leaves
// metadata alone omits it.
func TestAgentResource_metadataUpdateSendsOnlyChanges(t *testing.T) {
	t.Parallel()
	srv := newAgentsFake(t)
	full, fullValues := sixteenKeys("k16")
	renamed, renamedValues := sixteenKeys("renamed")
	stored := map[string]any{}
	for k, v := range renamedValues {
		stored[k] = v
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             checkAgentsArchived(srv),
		Steps: []resource.TestStep{
			{
				Config:            agentConfig(full),
				ConfigPlanChecks:  expectAgentAction(plancheck.ResourceActionCreate),
				ConfigStateChecks: expectMetadata(fullValues),
			},
			{
				Config:            agentConfig(renamed),
				ConfigPlanChecks:  expectAgentAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectMetadata(renamedValues),
				Check:             expectStoredMetadata(srv, stored),
			},
			{
				Config:            agentConfig(renamed + "  description = \"changed\"\n"),
				ConfigPlanChecks:  expectAgentAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectMetadata(renamedValues),
				Check:             expectStoredMetadata(srv, stored),
			},
		},
	})

	srv.AssertJSONBodies(routeUpdateAgent,
		agentUpdateBody(1, `{"k16":null,"renamed":"v16"}`, ""),
		`{"name":"test-agent","description":"changed","system":"","version":2,`+
			`"model":{"id":"claude-sonnet-4-6"},"mcp_servers":[],"skills":[],"tools":[]}`)
	srv.assertRequestShape(t)
}
