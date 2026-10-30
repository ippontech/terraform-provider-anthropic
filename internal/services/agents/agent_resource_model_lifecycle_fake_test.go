// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents_test

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// Lifecycle dates of https://platform.claude.com/docs/en/about-claude/model-deprecations.
const (
	deprecatedModel = "claude-sonnet-4-5-20250929"
	retiredModel    = "claude-opus-4-1-20250805"
)

func (f *agentsFake) deprecate(id string) {
	f.setModel(id, "deprecated", "2026-09-30T00:00:00Z", "2026-11-30T00:00:00Z")
}

func (f *agentsFake) retire(id string) {
	f.setModel(id, "retired", "2026-06-05T00:00:00Z", "2026-08-05T00:00:00Z")
}

// A deprecated model warns at plan time without blocking: the agents are
// created. The two agents on it share one lookup per plan, as many as the
// single agent on an active model gets: Terraform plans each step several
// times (the plan, the apply's own, the two after it), each with a fresh
// provider configuration and so a fresh cache, and never once per agent.
func TestAgentResource_deprecatedModelWarns(t *testing.T) {
	t.Parallel()
	srv := newAgentsFake(t)
	srv.deprecate(deprecatedModel)
	config := providertest.ProviderConfig(sdktest.APIKey) + `
resource "anthropic_agent" "a" {
  name  = "a"
  model = "` + deprecatedModel + `"
}
resource "anthropic_agent" "b" {
  name  = "b"
  model = "` + deprecatedModel + `"
}
resource "anthropic_agent" "c" {
  name  = "c"
  model = "claude-sonnet-4-6"
}
`
	created := []plancheck.PlanCheck{}
	for _, name := range []string{"a", "b", "c"} {
		created = append(created, plancheck.ExpectResourceAction("anthropic_agent."+name, plancheck.ResourceActionCreate))
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             checkAgentsArchived(srv),
		Steps: []resource.TestStep{{
			Config: config,
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PreApply:             created,
				PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
			},
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("anthropic_agent.a", tfjsonpath.New("model"), str(deprecatedModel)),
				statecheck.ExpectKnownValue("anthropic_agent.b", tfjsonpath.New("model"), str(deprecatedModel)),
				statecheck.ExpectKnownValue("anthropic_agent.b", tfjsonpath.New("id"), knownvalue.NotNull()),
			},
		}},
	})

	srv.AssertCalled(routeCreateAgent, 3)
	deprecated, active := srv.modelLookups(deprecatedModel), srv.modelLookups("claude-sonnet-4-6")
	t.Logf("lookups over the step's plans: %d of %s (two agents), %d of claude-sonnet-4-6 (one agent)", deprecated, deprecatedModel, active)
	if deprecated == 0 || deprecated != active {
		t.Errorf("%d lookups of %s for two agents, want one per plan: %d, as for the one agent on claude-sonnet-4-6", deprecated, deprecatedModel, active)
	}
	srv.AssertNoBeta(routeGetModel)
	srv.AssertCredential(routeGetModel, sdktest.APIKey)
	srv.assertRequestShape(t)
}

// A retired model fails the plan of a create, whether it is the agent's
// model or its advisor's, before any request to the agents API: the one plan
// makes one lookup per model.
func TestAgentResource_retiredModelFailsAtPlan(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		config  string
		lookups int
	}{
		"model": {
			config: providertest.ProviderConfig(sdktest.APIKey) + `
resource "anthropic_agent" "test" {
  name  = "test-agent"
  model = "` + retiredModel + `"
}
`,
			lookups: 1,
		},
		"advisor": {
			config: agentConfig(`  multiagent = {
    type    = "multiagent_20261001"
    advisor = { model = "` + retiredModel + `" }
  }
`),
			lookups: 2,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newAgentsFake(t)
			srv.retire(retiredModel)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
				Steps: []resource.TestStep{{
					Config: tc.config,
					ExpectError: regexp.MustCompile(`(?s)Retired\s+Model.*"claude-opus-4-1-20250805"\s+is\s+retired\s+` +
						`\(deprecated_at\s+2026-06-05T00:00:00Z,\s+retires_at\s+2026-08-05T00:00:00Z\).*applying\s+this\s+plan\s+would\s+fail`),
				}},
			})
			for _, route := range allAgentRoutes {
				srv.AssertCalled(route, 0)
			}
			srv.AssertCalled(routeGetModel, tc.lookups)
		})
	}
}

// An agent whose model retires between two applies still plans and applies
// an unrelated change: the retired model kept is a warning, not an error.
// The update re-sends the model (the API replaces it as a whole).
func TestAgentResource_modelRetiredAfterCreate(t *testing.T) {
	t.Parallel()
	srv := newAgentsFake(t)
	srv.setModel(retiredModel, "active", nil, nil)
	config := func(extra string) string {
		return providertest.ProviderConfig(sdktest.APIKey) + `
resource "anthropic_agent" "test" {
  name  = "test-agent"
  model = "` + retiredModel + `"
` + extra + `}
`
	}
	lookupsWhileActive := 0

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             checkAgentsArchived(srv),
		Steps: []resource.TestStep{
			{
				Config:            config(""),
				ConfigPlanChecks:  expectAgentCreated,
				ConfigStateChecks: []statecheck.StateCheck{expectAttr("model", str(retiredModel))},
			},
			{
				PreConfig: func() {
					lookupsWhileActive = srv.modelLookups(retiredModel)
					srv.retire(retiredModel)
				},
				Config:           config("  description = \"changed\"\n"),
				ConfigPlanChecks: expectAgentAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: []statecheck.StateCheck{
					expectAttr("model", str(retiredModel)),
					expectAttr("description", str("changed")),
					expectAttr("version", knownvalue.Int64Exact(2)),
				},
			},
		},
	})

	srv.AssertCalled(routeCreateAgent, 1)
	srv.AssertJSONBodies(routeUpdateAgent, `{"name":"test-agent","description":"changed","system":"","version":1,
		"model":{"id":"`+retiredModel+`","effort":"high"},"mcp_servers":[],"skills":[],"tools":[]}`)
	if after := srv.modelLookups(retiredModel); lookupsWhileActive == 0 || after <= lookupsWhileActive {
		t.Errorf("lookups of %s: %d while active, %d in all; want the second step's plans to look the retired model up",
			retiredModel, lookupsWhileActive, after)
	}
	srv.assertRequestShape(t)
}
