// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccMessageResource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccMessageResourceConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_message.test", "id"),
					resource.TestCheckResourceAttrSet("anthropic_message.test", "content"),
					resource.TestCheckResourceAttrSet("anthropic_message.test", "stop_reason"),
					resource.TestCheckResourceAttrSet("anthropic_message.test", "input_tokens"),
					resource.TestCheckResourceAttrSet("anthropic_message.test", "output_tokens"),
					resource.TestCheckResourceAttr("anthropic_message.test", "model", "claude-haiku-4-5-20251001"),
					resource.TestCheckResourceAttr("anthropic_message.test", "max_tokens", "128"),
					resource.TestCheckResourceAttr("anthropic_message.test", "messages.0.role", "user"),
					resource.TestCheckResourceAttr("anthropic_message.test", "messages.0.content", "Reply with the single word: pong"),
				),
			},
		},
	})
}

func TestAccMessageResourceWithSystemAndTemperature(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccMessageResourceWithOptionalConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_message.test_optional", "id"),
					resource.TestCheckResourceAttrSet("anthropic_message.test_optional", "content"),
					resource.TestCheckResourceAttr("anthropic_message.test_optional", "system", "You are a concise assistant."),
					resource.TestCheckResourceAttr("anthropic_message.test_optional", "temperature", "0.5"),
				),
			},
		},
	})
}

const testAccMessageResourceConfig = `
resource "anthropic_message" "test" {
  model      = "claude-haiku-4-5-20251001"
  max_tokens = 128

  messages = [
    {
      role    = "user"
      content = "Reply with the single word: pong"
    }
  ]
}
`

const testAccMessageResourceWithOptionalConfig = `
resource "anthropic_message" "test_optional" {
  model      = "claude-haiku-4-5-20251001"
  max_tokens = 128
  system     = "You are a concise assistant."
  temperature = 0.5

  messages = [
    {
      role    = "user"
      content = "Say hello in one word."
    }
  ]
}
`
