// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccAgentResource_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
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
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
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
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
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
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
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
