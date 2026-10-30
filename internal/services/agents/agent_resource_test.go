// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents_test

import (
	"context"
	"fmt"
	"maps"
	"testing"
	"time"

	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func testAccCheckAgentDestroyed(s *terraform.State) error {
	client := acctest.NewAPIKeyClient()
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "anthropic_agent" {
			continue
		}
		agent, err := client.Beta.Agents.Get(context.Background(), rs.Primary.ID, anthropic.BetaAgentGetParams{})
		if err != nil {
			// Resource not found — destroyed successfully.
			return nil
		}
		if !agent.ArchivedAt.IsZero() {
			// Agent is archived — our Delete succeeded.
			return nil
		}
		return fmt.Errorf("agent %s still exists and is not archived", rs.Primary.ID)
	}
	return nil
}

func TestAccAgentResource_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckAgentDestroyed,
		Steps: []resource.TestStep{
			// Create and Read
			{
				Config: testAccAgentResourceBasicConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_agent.test", "id"),
					resource.TestCheckResourceAttr("anthropic_agent.test", "model", "claude-sonnet-4-6"),
					resource.TestCheckResourceAttr("anthropic_agent.test", "name", "tf-acc-test-basic"),
					resource.TestCheckResourceAttrSet("anthropic_agent.test", "version"),
					resource.TestCheckResourceAttrSet("anthropic_agent.test", "created_at"),
					resource.TestCheckResourceAttrSet("anthropic_agent.test", "updated_at"),
				),
			},
			// ImportState
			{
				ResourceName:      "anthropic_agent.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
			// Update name
			{
				Config: testAccAgentResourceBasicConfigUpdated,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("anthropic_agent.test", "name", "tf-acc-test-basic-updated"),
					resource.TestCheckResourceAttr("anthropic_agent.test", "description", "Updated description"),
				),
			},
		},
	})
}

func TestAccAgentResource_modelEffort(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckAgentDestroyed,
		Steps: []resource.TestStep{
			// Omitted: the API resolves a per-model default and it lands in state.
			{
				Config: testAccAgentResourceEffortConfig(""),
				Check:  resource.TestCheckResourceAttrSet("anthropic_agent.test", "model_effort"),
			},
			// Explicit value.
			{
				Config: testAccAgentResourceEffortConfig(`model_effort = "high"`),
				Check:  resource.TestCheckResourceAttr("anthropic_agent.test", "model_effort", "high"),
			},
			// Update.
			{
				Config: testAccAgentResourceEffortConfig(`model_effort = "low"`),
				Check:  resource.TestCheckResourceAttr("anthropic_agent.test", "model_effort", "low"),
			},
			// Removing the attribute keeps the last value (Optional+Computed).
			{
				Config: testAccAgentResourceEffortConfig(""),
				Check:  resource.TestCheckResourceAttr("anthropic_agent.test", "model_effort", "low"),
			},
			// Changing model without model_effort resolves the new default.
			{
				Config: testAccAgentResourceEffortConfigModel("claude-opus-4-5", ""),
				Check:  resource.TestCheckResourceAttrSet("anthropic_agent.test", "model_effort"),
			},
			{
				ResourceName:      "anthropic_agent.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func TestAccAgentResource_modelInferenceGeo(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckAgentDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccAgentResourceEffortConfig(`model_inference_geo = "us"`),
				Check:  resource.TestCheckResourceAttr("anthropic_agent.test", "model_inference_geo", "us"),
			},
			{
				ResourceName:      "anthropic_agent.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
			// Removing the attribute clears the pin (whole-object model update).
			{
				Config: testAccAgentResourceEffortConfig(""),
				Check:  resource.TestCheckNoResourceAttr("anthropic_agent.test", "model_inference_geo"),
			},
		},
	})
}

func testAccAgentResourceEffortConfig(extra string) string {
	return testAccAgentResourceEffortConfigModel("claude-sonnet-4-6", extra)
}

func testAccAgentResourceEffortConfigModel(model, extra string) string {
	return fmt.Sprintf(`
resource "anthropic_agent" "test" {
  model = %q
  name  = "tf-acc-test-effort"
  %s
}
`, model, extra)
}

func TestAccAgentResource_withSystemAndDescription(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckAgentDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccAgentResourceWithSystemConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_agent.test_system", "id"),
					resource.TestCheckResourceAttr("anthropic_agent.test_system", "model", "claude-haiku-4-5-20251001"),
					resource.TestCheckResourceAttr("anthropic_agent.test_system", "name", "tf-acc-test-system"),
					resource.TestCheckResourceAttr("anthropic_agent.test_system", "description", "A test agent"),
					resource.TestCheckResourceAttr("anthropic_agent.test_system", "system", "You are a helpful assistant."),
				),
			},
		},
	})
}

func TestAccAgentResource_withMetadata(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckAgentDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccAgentResourceWithMetadataConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_agent.test_meta", "id"),
					resource.TestCheckResourceAttr("anthropic_agent.test_meta", "metadata.env", "test"),
					resource.TestCheckResourceAttr("anthropic_agent.test_meta", "metadata.team", "platform"),
				),
			},
		},
	})
}

// Removing metadata keys deletes them server-side, not only from state: the
// API applies metadata as a patch and keeps an omitted key, so the update
// sends a removed key as null
// (https://platform.claude.com/docs/en/api/beta/agents/update), and
// `metadata = {}` stays an empty map in state although the API answers {}
// with or without metadata. The live counterpart of the fake-API
// TestAgentResource_metadataKeyRemoved, plus the removal of the attribute;
// the post-apply plan of every step must be empty (the default check).
func TestAccAgentResource_metadataKeyRemoved(t *testing.T) {
	name := fmt.Sprintf("tf-acc-test-agent-metadata-%d", time.Now().UnixNano())
	// In place, never a replacement: the out-of-band reads must all see the
	// agent the first step created.
	updateInPlace := resource.ConfigPlanChecks{
		PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(agentAddr, plancheck.ResourceActionUpdate)},
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckAgentDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccAgentResourceMetadataConfig(name, `metadata = { team = "platform", env = "test" }`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(agentAddr, "metadata.%", "2"),
					resource.TestCheckResourceAttr(agentAddr, "metadata.team", "platform"),
					resource.TestCheckResourceAttr(agentAddr, "metadata.env", "test"),
					testAccCheckAgentStoredMetadata(map[string]string{"team": "platform", "env": "test"}),
				),
			},
			// Remove one key: sent as null, deleted server-side.
			{
				Config:           testAccAgentResourceMetadataConfig(name, `metadata = { team = "platform" }`),
				ConfigPlanChecks: updateInPlace,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(agentAddr, "metadata.%", "1"),
					resource.TestCheckResourceAttr(agentAddr, "metadata.team", "platform"),
					resource.TestCheckNoResourceAttr(agentAddr, "metadata.env"),
					testAccCheckAgentStoredMetadata(map[string]string{"team": "platform"}),
				),
			},
			// metadata = {}: the last key is deleted and the empty map stays in
			// state (the `metadata.%` check accepts a null map too, but
			// Terraform fails the step unless the state is the configured
			// empty map).
			{
				Config:           testAccAgentResourceMetadataConfig(name, `metadata = {}`),
				ConfigPlanChecks: updateInPlace,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(agentAddr, "metadata.%", "0"),
					resource.TestCheckNoResourceAttr(agentAddr, "metadata.team"),
					testAccCheckAgentStoredMetadata(map[string]string{}),
				),
			},
			// Remove the attribute: metadata goes back to null in state.
			{
				Config:           testAccAgentResourceMetadataConfig(name, ""),
				ConfigPlanChecks: updateInPlace,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr(agentAddr, "metadata.%"),
					testAccCheckAgentStoredMetadata(map[string]string{}),
				),
			},
		},
	})
}

// testAccAgentResourceMetadataConfig is an agent named name, on the cheapest
// model the tests use, with extra appended to its attributes.
func testAccAgentResourceMetadataConfig(name, extra string) string {
	return fmt.Sprintf(`
resource "anthropic_agent" "test" {
  model = "claude-haiku-4-5-20251001"
  name  = %q
  %s
}
`, name, extra)
}

// testAccCheckAgentStoredMetadata reads the agent at agentAddr (fake_test.go)
// through the API, outside the provider, and checks that its metadata is
// exactly want: a key removed from the configuration must be gone
// server-side, not only from state.
func testAccCheckAgentStoredMetadata(want map[string]string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[agentAddr]
		if !ok {
			return fmt.Errorf("%s not found in state", agentAddr)
		}
		agent, err := acctest.NewAPIKeyClient().Beta.Agents.Get(context.Background(), rs.Primary.ID, anthropic.BetaAgentGetParams{})
		if err != nil {
			return fmt.Errorf("reading agent %s: %w", rs.Primary.ID, err)
		}
		if !maps.Equal(agent.Metadata, want) {
			return fmt.Errorf("agent %s has metadata %v on the API, want %v", rs.Primary.ID, agent.Metadata, want)
		}
		return nil
	}
}

func TestAccAgentResource_withAgentToolset(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckAgentDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccAgentResourceWithToolsetConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_agent.test_toolset", "id"),
					resource.TestCheckResourceAttr("anthropic_agent.test_toolset", "agent_toolset.default_enabled", "true"),
					resource.TestCheckResourceAttr("anthropic_agent.test_toolset", "agent_toolset.default_permission_policy", "always_allow"),
				),
			},
			// A per-tool override that leaves `enabled` unset: the post-apply plan
			// must be empty, i.e. the inherited value the API echoes back maps to
			// null rather than surfacing as drift (mapToolConfigsToState).
			{
				Config: testAccAgentResourceWithToolsetConfigsConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("anthropic_agent.test_toolset", "agent_toolset.configs.#", "1"),
					resource.TestCheckResourceAttr("anthropic_agent.test_toolset", "agent_toolset.configs.0.name", "bash"),
					resource.TestCheckResourceAttr("anthropic_agent.test_toolset", "agent_toolset.configs.0.permission_policy", "always_ask"),
					resource.TestCheckNoResourceAttr("anthropic_agent.test_toolset", "agent_toolset.configs.0.enabled"),
				),
			},
		},
	})
}

func TestAccAgentResource_withCustomTools(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckAgentDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccAgentResourceWithCustomToolsConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_agent.test_custom_tools", "id"),
					resource.TestCheckResourceAttr("anthropic_agent.test_custom_tools", "custom_tools.#", "1"),
					resource.TestCheckResourceAttr("anthropic_agent.test_custom_tools", "custom_tools.0.name", "lookup_user"),
					resource.TestCheckResourceAttr("anthropic_agent.test_custom_tools", "custom_tools.0.description", "Look up a user by their email address"),
				),
			},
		},
	})
}

func TestAccAgentResource_withSkills(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckAgentDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccAgentResourceWithSkillsConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_agent.test_skills", "id"),
					resource.TestCheckResourceAttr("anthropic_agent.test_skills", "skills.#", "1"),
					resource.TestCheckResourceAttr("anthropic_agent.test_skills", "skills.0.type", "anthropic"),
					resource.TestCheckResourceAttr("anthropic_agent.test_skills", "skills.0.skill_id", "xlsx"),
				),
			},
		},
	})
}

const testAccAgentResourceBasicConfig = `
resource "anthropic_agent" "test" {
  model = "claude-sonnet-4-6"
  name  = "tf-acc-test-basic"
}
`

const testAccAgentResourceBasicConfigUpdated = `
resource "anthropic_agent" "test" {
  model       = "claude-sonnet-4-6"
  name        = "tf-acc-test-basic-updated"
  description = "Updated description"
}
`

const testAccAgentResourceWithSystemConfig = `
resource "anthropic_agent" "test_system" {
  model       = "claude-haiku-4-5-20251001"
  name        = "tf-acc-test-system"
  description = "A test agent"
  system      = "You are a helpful assistant."
}
`

const testAccAgentResourceWithMetadataConfig = `
resource "anthropic_agent" "test_meta" {
  model = "claude-sonnet-4-6"
  name  = "tf-acc-test-metadata"

  metadata = {
    env  = "test"
    team = "platform"
  }
}
`

const testAccAgentResourceWithToolsetConfig = `
resource "anthropic_agent" "test_toolset" {
  model = "claude-sonnet-4-6"
  name  = "tf-acc-test-toolset"

  agent_toolset = {
    default_enabled           = true
    default_permission_policy = "always_allow"
  }
}
`

const testAccAgentResourceWithToolsetConfigsConfig = `
resource "anthropic_agent" "test_toolset" {
  model = "claude-sonnet-4-6"
  name  = "tf-acc-test-toolset"

  agent_toolset = {
    default_enabled           = true
    default_permission_policy = "always_allow"
    configs = [
      {
        name              = "bash"
        permission_policy = "always_ask"
      }
    ]
  }
}
`

const testAccAgentResourceWithCustomToolsConfig = `
resource "anthropic_agent" "test_custom_tools" {
  model = "claude-sonnet-4-6"
  name  = "tf-acc-test-custom-tools"

  custom_tools = [
    {
      name        = "lookup_user"
      description = "Look up a user by their email address"
      input_schema = jsonencode({
        type = "object"
        properties = {
          email = {
            type        = "string"
            description = "The user's email address"
          }
        }
        required = ["email"]
      })
    }
  ]
}
`

const testAccAgentResourceWithSkillsConfig = `
resource "anthropic_agent" "test_skills" {
  model = "claude-sonnet-4-6"
  name  = "tf-acc-test-skills"

  skills = [
    {
      type     = "anthropic"
      skill_id = "xlsx"
    }
  ]
}
`

func TestAccAgentResource_multiagent(t *testing.T) {
	const coord = "anthropic_agent.coordinator"
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckAgentDestroyed,
		Steps: []resource.TestStep{
			// Create: a member agent plus self.
			{
				Config: testAccAgentResourceMultiagentConfig("a", "self_last"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(coord, "multiagent.type", "coordinator"),
					resource.TestCheckResourceAttr(coord, "multiagent.agents.#", "2"),
					resource.TestCheckResourceAttr(coord, "multiagent.agents.0.type", "agent"),
					resource.TestCheckResourceAttrPair(coord, "multiagent.agents.0.id", "anthropic_agent.member_a", "id"),
					resource.TestCheckResourceAttrSet(coord, "multiagent.agents.0.version"),
					resource.TestCheckResourceAttr(coord, "multiagent.agents.1.type", "self"),
				),
			},
			// Import: a self entry cannot be told apart from an explicit reference
			// without configuration, so the roster is not compared.
			{
				ResourceName:            coord,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"multiagent"},
			},
			// Update: reorder (self first) and swap the member; correlation is by key.
			{
				Config: testAccAgentResourceMultiagentConfig("b", "self_first"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(coord, "multiagent.agents.#", "2"),
					resource.TestCheckResourceAttr(coord, "multiagent.agents.0.type", "self"),
					resource.TestCheckResourceAttr(coord, "multiagent.agents.1.type", "agent"),
					resource.TestCheckResourceAttrPair(coord, "multiagent.agents.1.id", "anthropic_agent.member_b", "id"),
				),
			},
			// Remove the block: sends multiagent null.
			{
				Config: testAccAgentResourceMultiagentConfig("b", "none"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr(coord, "multiagent.type"),
					resource.TestCheckNoResourceAttr(coord, "multiagent.agents.#"),
				),
			},
		},
	})
}

func testAccAgentResourceMultiagentConfig(member, layout string) string {
	var block string
	switch layout {
	case "self_last":
		block = fmt.Sprintf(`
  multiagent = {
    type = "coordinator"
    agents = [
      { type = "agent", id = anthropic_agent.member_%s.id },
      { type = "self" },
    ]
  }`, member)
	case "self_first":
		block = fmt.Sprintf(`
  multiagent = {
    type = "coordinator"
    agents = [
      { type = "self" },
      { type = "agent", id = anthropic_agent.member_%s.id },
    ]
  }`, member)
	}
	return fmt.Sprintf(`
resource "anthropic_agent" "member_a" {
  model = "claude-sonnet-4-6"
  name  = "tf-acc-test-multiagent-member-a"
}

resource "anthropic_agent" "member_b" {
  model = "claude-sonnet-4-6"
  name  = "tf-acc-test-multiagent-member-b"
}

resource "anthropic_agent" "coordinator" {
  model = "claude-sonnet-4-6"
  name  = "tf-acc-test-multiagent-coordinator"
%s
}
`, block)
}

// An unrelated update of the coordinator must not re-pin an unpinned member to the
// member's newer version.
func TestAccAgentResource_multiagentKeepsPinnedVersion(t *testing.T) {
	const coord = "anthropic_agent.coordinator"
	cfg := func(memberDesc, coordDesc string) string {
		return fmt.Sprintf(`
resource "anthropic_agent" "member" {
  model       = "claude-sonnet-4-6"
  name        = "tf-acc-test-multiagent-pin-member"
  description = %q
}

resource "anthropic_agent" "coordinator" {
  model       = "claude-sonnet-4-6"
  name        = "tf-acc-test-multiagent-pin-coordinator"
  description = %q

  multiagent = {
    type   = "coordinator"
    agents = [{ type = "agent", id = anthropic_agent.member.id }]
  }
}
`, memberDesc, coordDesc)
	}
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckAgentDestroyed,
		Steps: []resource.TestStep{
			{
				Config: cfg("v1", "c1"),
				Check:  resource.TestCheckResourceAttr(coord, "multiagent.agents.0.version", "1"),
			},
			// Bump the member to version 2; the coordinator is untouched.
			{
				Config: cfg("v2", "c1"),
				Check:  resource.TestCheckResourceAttr("anthropic_agent.member", "version", "2"),
			},
			// Edit only the coordinator: the roster must keep the member at version 1.
			{
				Config: cfg("v2", "c2"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(coord, "description", "c2"),
					resource.TestCheckResourceAttr(coord, "multiagent.agents.0.version", "1"),
				),
			},
		},
	})
}
