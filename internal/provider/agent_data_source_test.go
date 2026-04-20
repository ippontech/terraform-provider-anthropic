// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccAgentDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckAgentDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccAgentDataSourceConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.anthropic_agent.test", "id"),
					resource.TestCheckResourceAttrSet("data.anthropic_agent.test", "name"),
					resource.TestCheckResourceAttrSet("data.anthropic_agent.test", "model"),
					resource.TestCheckResourceAttrSet("data.anthropic_agent.test", "version"),
					resource.TestCheckResourceAttrSet("data.anthropic_agent.test", "created_at"),
					resource.TestCheckResourceAttrSet("data.anthropic_agent.test", "updated_at"),
					resource.TestCheckResourceAttrPair("data.anthropic_agent.test", "id", "anthropic_agent.test", "id"),
					resource.TestCheckResourceAttrPair("data.anthropic_agent.test", "name", "anthropic_agent.test", "name"),
				),
			},
		},
	})
}

const testAccAgentDataSourceConfig = `
resource "anthropic_agent" "test" {
  model = "claude-sonnet-4-6"
  name  = "tf-acc-test-agent-ds"
}

data "anthropic_agent" "test" {
  agent_id = anthropic_agent.test.id
}
`
