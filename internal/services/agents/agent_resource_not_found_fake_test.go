// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents_test

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
)

// agentID is the ID the fake gives to the n-th agent it creates.
func agentID(n int) string { return fmt.Sprintf("agent_%024d", n) }

// expectAgentCreated checks that the step creates the agent and that the
// refresh after the apply plans nothing.
var expectAgentCreated = providertest.ExpectAction(agentAddr, plancheck.ResourceActionCreate)

// An agent deleted outside Terraform is dropped by the refresh and re-created
// from the configuration by the next apply, instead of failing the refresh
// with a 404.
func TestAgentResource_deletedOutOfBandIsRecreated(t *testing.T) {
	t.Parallel()
	srv := newAgentsFake(t)
	config := agentConfig("  description = \"re-created after an out-of-band delete\"\n")
	expectAgent := func(id string) []statecheck.StateCheck {
		return []statecheck.StateCheck{
			statecheck.ExpectKnownValue(agentAddr, tfjsonpath.New("id"), knownvalue.StringExact(id)),
			statecheck.ExpectKnownValue(agentAddr, tfjsonpath.New("description"), knownvalue.StringExact("re-created after an out-of-band delete")),
		}
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             checkAgentsArchived(srv),
		Steps: []resource.TestStep{
			{
				Config:            config,
				ConfigPlanChecks:  expectAgentCreated,
				ConfigStateChecks: expectAgent(agentID(1)),
			},
			{
				PreConfig: func() {
					if !srv.agents.Delete(agentID(1)) {
						t.Fatalf("%s is not in the fake", agentID(1))
					}
				},
				Config:            config,
				ConfigPlanChecks:  expectAgentCreated,
				ConfigStateChecks: expectAgent(agentID(2)),
			},
		},
	})

	// The refresh of step 2 met the 404: two creates, no update, and only the
	// second agent archived by the final destroy.
	srv.AssertCalled(routeCreateAgent, 2)
	srv.AssertCalled(routeUpdateAgent, 0)
	srv.AssertCalled(routeArchiveAgent, 1)
	srv.assertRequestShape(t)
}

// Destroy succeeds when the agent is already gone: the archive answers 404
// although the refresh still found it (deleted between refresh and destroy,
// or destroy run with -refresh=false).
func TestAgentResource_destroyToleratesNotFound(t *testing.T) {
	t.Parallel()
	srv := newAgentsFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{
				Config:           agentConfig(""),
				ConfigPlanChecks: expectAgentCreated,
			},
			{
				PreConfig: func() { srv.FailNext(routeArchiveAgent, http.StatusNotFound, 1) },
				Config:    agentConfig(""),
				Destroy:   true,
			},
		},
	})

	// One archive, answered 404 and not retried.
	reqs := srv.Requests(routeArchiveAgent)
	if len(reqs) != 1 || reqs[0].Fault != "status 404" || reqs[0].Path != "/v1/agents/"+agentID(1)+"/archive" {
		t.Errorf("archive requests = %+v, want one to %s answered 404", reqs, agentID(1))
	}
	srv.assertRequestShape(t)
}
