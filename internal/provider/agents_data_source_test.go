// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccAgentsDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckAgentDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccAgentsDataSourceConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.anthropic_agents.test", "agents.#"),
					resource.TestCheckResourceAttrSet("data.anthropic_agents.test", "agents.0.id"),
					resource.TestCheckResourceAttrSet("data.anthropic_agents.test", "agents.0.name"),
					resource.TestCheckResourceAttrSet("data.anthropic_agents.test", "agents.0.model"),
					resource.TestCheckResourceAttrSet("data.anthropic_agents.test", "agents.0.version"),
					resource.TestCheckResourceAttrSet("data.anthropic_agents.test", "agents.0.created_at"),
					resource.TestCheckResourceAttrSet("data.anthropic_agents.test", "agents.0.updated_at"),
				),
			},
		},
	})
}

const testAccAgentsDataSourceConfig = `
resource "anthropic_agent" "test" {
  model = "claude-sonnet-4-6"
  name  = "tf-acc-test-agents-ds"
}

data "anthropic_agents" "test" {
  depends_on = [anthropic_agent.test]
}
`
