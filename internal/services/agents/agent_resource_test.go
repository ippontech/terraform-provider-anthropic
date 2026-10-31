// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents_test

import (
	"context"
	"fmt"
	"testing"

	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
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
			// Web options: the post-apply plan must be empty, i.e. the API echoes
			// them verbatim and no default comes back for the unset ones.
			{
				Config: testAccAgentResourceWithWebOptionsConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("anthropic_agent.test_toolset", "agent_toolset.configs.#", "2"),
					resource.TestCheckResourceAttr("anthropic_agent.test_toolset", "agent_toolset.configs.0.name", "web_search"),
					resource.TestCheckTypeSetElemAttr("anthropic_agent.test_toolset", "agent_toolset.configs.0.allowed_domains.*", "arxiv.org"),
					resource.TestCheckResourceAttr("anthropic_agent.test_toolset", "agent_toolset.configs.0.user_location.country", "US"),
					resource.TestCheckResourceAttr("anthropic_agent.test_toolset", "agent_toolset.configs.0.user_location.timezone", "America/Los_Angeles"),
					resource.TestCheckNoResourceAttr("anthropic_agent.test_toolset", "agent_toolset.configs.0.user_location.city"),
					resource.TestCheckResourceAttr("anthropic_agent.test_toolset", "agent_toolset.configs.1.name", "web_fetch"),
					resource.TestCheckTypeSetElemAttr("anthropic_agent.test_toolset", "agent_toolset.configs.1.blocked_domains.*", "ads.example.com"),
					resource.TestCheckResourceAttr("anthropic_agent.test_toolset", "agent_toolset.configs.1.max_content_tokens", "50000"),
				),
			},
			// Swapping a list and dropping options: tools are replaced as a whole
			// on update, so the removed values must not come back.
			{
				Config: testAccAgentResourceWithWebOptionsUpdatedConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("anthropic_agent.test_toolset", "agent_toolset.configs.0.allowed_domains"),
					resource.TestCheckTypeSetElemAttr("anthropic_agent.test_toolset", "agent_toolset.configs.0.blocked_domains.*", "ads.example.com"),
					resource.TestCheckNoResourceAttr("anthropic_agent.test_toolset", "agent_toolset.configs.0.user_location"),
					resource.TestCheckNoResourceAttr("anthropic_agent.test_toolset", "agent_toolset.configs.1.blocked_domains"),
					resource.TestCheckResourceAttr("anthropic_agent.test_toolset", "agent_toolset.configs.1.max_content_tokens", "10000"),
				),
			},
			// The auto policy as the default and as an override: the post-apply
			// plan must be empty, and the override left unset maps to null.
			{
				Config: testAccAgentResourceWithAutoPolicyConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("anthropic_agent.test_toolset", "agent_toolset.default_permission_policy", "auto"),
					resource.TestCheckResourceAttr("anthropic_agent.test_toolset", "agent_toolset.configs.0.permission_policy", "always_ask"),
					resource.TestCheckNoResourceAttr("anthropic_agent.test_toolset", "agent_toolset.configs.1.permission_policy"),
				),
			},
			// url_sources, with an explicit `all`: the post-apply plan must be
			// empty, i.e. the API echoes every key in the form that was sent.
			{
				Config: testAccAgentResourceWithURLSourcesConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("anthropic_agent.test_toolset", "agent_toolset.configs.0.url_sources.user_input", "all"),
					resource.TestCheckResourceAttr("anthropic_agent.test_toolset", "agent_toolset.configs.0.url_sources.client_tool_results.type", "none"),
					resource.TestCheckNoResourceAttr("anthropic_agent.test_toolset", "agent_toolset.configs.0.url_sources.client_tool_results.tools"),
					resource.TestCheckResourceAttr("anthropic_agent.test_toolset", "agent_toolset.configs.0.url_sources.server_tool_results.type", "only"),
					resource.TestCheckTypeSetElemAttr("anthropic_agent.test_toolset", "agent_toolset.configs.0.url_sources.server_tool_results.tools.*", "web_search"),
				),
			},
			// Removing url_sources must not leave it behind (tools are replaced as
			// a whole on update).
			{
				Config: testAccAgentResourceWithAutoPolicyConfig,
				Check:  resource.TestCheckNoResourceAttr("anthropic_agent.test_toolset", "agent_toolset.configs.1.url_sources"),
			},
		},
	})
}

// The auto policy on an MCP toolset default and on an MCP tool override. The
// MCP server is never reached: agents are only validated against it when a
// session starts.
func TestAccAgentResource_mcpToolsetAutoPolicy(t *testing.T) {
	const name = "anthropic_agent.test_mcp"
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckAgentDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccAgentResourceMCPAutoPolicyConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(name, "mcp_toolsets.0.default_permission_policy", "auto"),
					resource.TestCheckResourceAttr(name, "mcp_toolsets.0.configs.0.permission_policy", "always_ask"),
					resource.TestCheckResourceAttr(name, "mcp_toolsets.0.configs.1.permission_policy", "auto"),
				),
			},
			{
				ResourceName:      name,
				ImportState:       true,
				ImportStateVerify: true,
				// Import has no configuration to tell an echoed default from an
				// explicit override, so per-tool fields come back verbatim.
				ImportStateVerifyIgnore: []string{"mcp_toolsets.0.configs"},
			},
		},
	})
}

// The service account set explicitly, then removed: the API returns it for
// every agent, so both post-apply plans must be empty. An aws_role identity is
// only usable on Claude Platform on AWS and is covered by unit tests only.
func TestAccAgentResource_executionIdentity(t *testing.T) {
	const name = "anthropic_agent.test"
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckAgentDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccAgentResourceEffortConfig(`execution_identity = { type = "service_account" }`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(name, "execution_identity.type", "service_account"),
					resource.TestCheckNoResourceAttr(name, "execution_identity.role_arn"),
				),
			},
			// Import cannot tell an explicit default from an unset attribute.
			{
				ResourceName:            name,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"execution_identity"},
			},
			{
				Config: testAccAgentResourceEffortConfig(""),
				Check:  resource.TestCheckNoResourceAttr(name, "execution_identity.type"),
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

const testAccAgentResourceWithWebOptionsConfig = `
resource "anthropic_agent" "test_toolset" {
  model = "claude-sonnet-4-6"
  name  = "tf-acc-test-toolset"

  agent_toolset = {
    default_enabled           = true
    default_permission_policy = "always_allow"
    configs = [
      {
        name            = "web_search"
        allowed_domains = ["docs.example.com", "arxiv.org"]
        user_location = {
          country  = "US"
          timezone = "America/Los_Angeles"
        }
      },
      {
        name               = "web_fetch"
        blocked_domains    = ["ads.example.com"]
        max_content_tokens = 50000
      }
    ]
  }
}
`

const testAccAgentResourceWithWebOptionsUpdatedConfig = `
resource "anthropic_agent" "test_toolset" {
  model = "claude-sonnet-4-6"
  name  = "tf-acc-test-toolset"

  agent_toolset = {
    default_enabled           = true
    default_permission_policy = "always_allow"
    configs = [
      {
        name            = "web_search"
        blocked_domains = ["ads.example.com"]
      },
      {
        name               = "web_fetch"
        max_content_tokens = 10000
      }
    ]
  }
}
`

const testAccAgentResourceWithAutoPolicyConfig = `
resource "anthropic_agent" "test_toolset" {
  model = "claude-sonnet-4-6"
  name  = "tf-acc-test-toolset"

  agent_toolset = {
    default_enabled           = true
    default_permission_policy = "auto"
    configs = [
      {
        name              = "bash"
        permission_policy = "always_ask"
      },
      {
        name               = "web_fetch"
        max_content_tokens = 10000
      }
    ]
  }
}
`

const testAccAgentResourceWithURLSourcesConfig = `
resource "anthropic_agent" "test_toolset" {
  model = "claude-sonnet-4-6"
  name  = "tf-acc-test-toolset"

  agent_toolset = {
    default_enabled           = true
    default_permission_policy = "auto"
    configs = [
      {
        name = "web_fetch"
        url_sources = {
          user_input          = "all"
          client_tool_results = { type = "none" }
          server_tool_results = { type = "only", tools = ["web_search"] }
        }
      }
    ]
  }
}
`

const testAccAgentResourceMCPAutoPolicyConfig = `
resource "anthropic_agent" "test_mcp" {
  model = "claude-sonnet-4-6"
  name  = "tf-acc-test-mcp-auto"

  mcp_servers = [
    {
      name = "example"
      url  = "https://mcp.example.com/mcp"
    }
  ]

  mcp_toolsets = [
    {
      mcp_server_name           = "example"
      default_enabled           = true
      default_permission_policy = "auto"
      configs = [
        {
          name              = "delete_record"
          permission_policy = "always_ask"
        },
        {
          name              = "search"
          enabled           = true
          permission_policy = "auto"
        }
      ]
    }
  ]
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

// multiagent_20261001: create with predefined subagents only, change the
// members and fall back to the defaults, switch to the coordinator type, then
// remove the block. Every post-apply plan must be empty.
func TestAccAgentResource_multiagent20261001(t *testing.T) {
	const orch = "anthropic_agent.orchestrator"
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckAgentDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccAgentResourceMultiagent20261001Config("a", "restricted"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(orch, "multiagent.type", "multiagent_20261001"),
					resource.TestCheckResourceAttr(orch, "multiagent.subagents.inline_agents_enabled", "false"),
					resource.TestCheckResourceAttr(orch, "multiagent.subagents.predefined_agents.#", "2"),
					resource.TestCheckResourceAttrPair(orch, "multiagent.subagents.predefined_agents.0.id", "anthropic_agent.member_a", "id"),
					resource.TestCheckResourceAttrSet(orch, "multiagent.subagents.predefined_agents.0.version"),
					resource.TestCheckResourceAttr(orch, "multiagent.subagents.predefined_agents.1.type", "self"),
					resource.TestCheckResourceAttr(orch, "multiagent.workflows.enabled", "false"),
					resource.TestCheckNoResourceAttr(orch, "multiagent.advisor.model"),
				),
			},
			// Import: a self entry cannot be told apart from an explicit reference
			// without configuration, so the configuration is not compared.
			{
				ResourceName:            orch,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"multiagent"},
			},
			// Swap the member and drop every other setting: the defaults (inline
			// agents and workflows on) are sent explicitly and read back as null.
			{
				Config: testAccAgentResourceMultiagent20261001Config("b", "defaults"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(orch, "multiagent.subagents.predefined_agents.#", "1"),
					resource.TestCheckResourceAttrPair(orch, "multiagent.subagents.predefined_agents.0.id", "anthropic_agent.member_b", "id"),
					resource.TestCheckNoResourceAttr(orch, "multiagent.subagents.inline_agents_enabled"),
					resource.TestCheckNoResourceAttr(orch, "multiagent.workflows.enabled"),
				),
			},
			{
				Config: testAccAgentResourceMultiagent20261001Config("b", "coordinator"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(orch, "multiagent.type", "coordinator"),
					resource.TestCheckResourceAttr(orch, "multiagent.agents.#", "1"),
					resource.TestCheckNoResourceAttr(orch, "multiagent.subagents.predefined_agents.#"),
				),
			},
			{
				Config: testAccAgentResourceMultiagent20261001Config("b", "none"),
				Check:  resource.TestCheckNoResourceAttr(orch, "multiagent.type"),
			},
		},
	})
}

func testAccAgentResourceMultiagent20261001Config(member, layout string) string {
	var block string
	switch layout {
	case "restricted":
		block = fmt.Sprintf(`
  multiagent = {
    type = "multiagent_20261001"
    subagents = {
      inline_agents_enabled = false
      predefined_agents = [
        { type = "agent", id = anthropic_agent.member_%s.id },
        { type = "self" },
      ]
    }
    workflows = { enabled = false }
  }`, member)
	case "defaults":
		block = fmt.Sprintf(`
  multiagent = {
    type = "multiagent_20261001"
    subagents = {
      predefined_agents = [{ type = "agent", id = anthropic_agent.member_%s.id }]
    }
  }`, member)
	case "coordinator":
		block = fmt.Sprintf(`
  multiagent = {
    type   = "coordinator"
    agents = [{ type = "agent", id = anthropic_agent.member_%s.id }]
  }`, member)
	}
	return fmt.Sprintf(`
resource "anthropic_agent" "member_a" {
  model = "claude-sonnet-4-6"
  name  = "tf-acc-test-multiagent-20261001-member-a"
}

resource "anthropic_agent" "member_b" {
  model = "claude-sonnet-4-6"
  name  = "tf-acc-test-multiagent-20261001-member-b"
}

resource "anthropic_agent" "orchestrator" {
  model = "claude-sonnet-4-6"
  name  = "tf-acc-test-multiagent-20261001"
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
