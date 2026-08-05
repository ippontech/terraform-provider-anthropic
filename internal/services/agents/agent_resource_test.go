// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func testAccCheckAgentDestroyed(s *terraform.State) error {
	client := anthropic.NewClient(option.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")))
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

func TestAccAgentResource_withModelEffortAndMultiagent(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckAgentDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccAgentResourceWithModelEffortAndMultiagentConfig("medium"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_agent.worker", "id"),
					resource.TestCheckResourceAttr("anthropic_agent.worker", "model_effort", "low"),
					resource.TestCheckResourceAttrSet("anthropic_agent.coordinator", "id"),
					resource.TestCheckResourceAttr("anthropic_agent.coordinator", "model_effort", "medium"),
					resource.TestCheckResourceAttr("anthropic_agent.coordinator", "multiagent.type", "coordinator"),
					resource.TestCheckResourceAttr("anthropic_agent.coordinator", "multiagent.agents.#", "2"),
					resource.TestCheckResourceAttr("anthropic_agent.coordinator", "multiagent.agents.0.type", "self"),
					resource.TestCheckNoResourceAttr("anthropic_agent.coordinator", "multiagent.agents.0.id"),
					resource.TestCheckResourceAttr("anthropic_agent.coordinator", "multiagent.agents.1.type", "agent"),
					resource.TestCheckResourceAttrSet("anthropic_agent.coordinator", "multiagent.agents.1.id"),
					resource.TestCheckResourceAttrSet("anthropic_agent.coordinator", "multiagent.agents.1.version"),
				),
			},
			{
				Config: testAccAgentResourceWithModelEffortAndMultiagentConfig("high"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("anthropic_agent.coordinator", "model_effort", "high"),
					resource.TestCheckResourceAttr("anthropic_agent.coordinator", "multiagent.type", "coordinator"),
					resource.TestCheckResourceAttr("anthropic_agent.coordinator", "multiagent.agents.#", "2"),
				),
			},
			{
				Config: testAccAgentResourceWithoutMultiagentConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("anthropic_agent.coordinator", "model_effort", "high"),
					resource.TestCheckNoResourceAttr("anthropic_agent.coordinator", "multiagent"),
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

func testAccAgentResourceWithModelEffortAndMultiagentConfig(coordinatorEffort string) string {
	return fmt.Sprintf(`
resource "anthropic_agent" "worker" {
  model        = "claude-sonnet-4-6"
  model_effort = "low"
  name         = "tf-acc-test-multiagent-worker"
}

resource "anthropic_agent" "coordinator" {
  model        = "claude-sonnet-4-6"
  model_effort = %q
  name         = "tf-acc-test-multiagent-coordinator"

  multiagent = {
    type = "coordinator"
    agents = [
      {
        type = "self"
      },
      {
        type    = "agent"
        id      = anthropic_agent.worker.id
        version = anthropic_agent.worker.version
      }
    ]
  }
}
`, coordinatorEffort)
}

const testAccAgentResourceWithoutMultiagentConfig = `
resource "anthropic_agent" "worker" {
  model        = "claude-sonnet-4-6"
  model_effort = "low"
  name         = "tf-acc-test-multiagent-worker"
}

resource "anthropic_agent" "coordinator" {
  model        = "claude-sonnet-4-6"
  model_effort = "high"
  name         = "tf-acc-test-multiagent-coordinator"
}
`
