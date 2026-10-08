// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package messages_test

import (
	"encoding/json"
	"fmt"
	"testing"

	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccMessageResource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
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
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
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

func TestAccMessageResource_withThinking(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
resource "anthropic_message" "test_thinking" {
  model      = "claude-haiku-4-5-20251001"
  max_tokens = 2048

  system               = "Answer briefly."
  cache_control        = {}
  stop_sequences       = ["END_OF_ANSWER"]
  metadata             = { user_id = "terraform-acc-test" }

  thinking = {
    type          = "enabled"
    budget_tokens = 1024
  }

  messages = [
    {
      role    = "user"
      content = "What is 17 multiplied by 23?"
    }
  ]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("anthropic_message.test_thinking", "thinking.type", "enabled"),
					resource.TestCheckResourceAttr("anthropic_message.test_thinking", "thinking.budget_tokens", "1024"),
					resource.TestCheckResourceAttrSet("anthropic_message.test_thinking", "content"),
					resource.TestCheckResourceAttrSet("anthropic_message.test_thinking", "cache_read_input_tokens"),
					resource.TestCheckResourceAttrWith("anthropic_message.test_thinking", "thinking_tokens", func(v string) error {
						if v == "" || v == "0" {
							return fmt.Errorf("expected thinking_tokens > 0, got %q", v)
						}
						return nil
					}),
				),
			},
		},
	})
}

func TestAccMessageResource_withStructuredOutput(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `
resource "anthropic_message" "test_structured" {
  model      = "claude-haiku-4-5-20251001"
  max_tokens = 256

  output_config = {
    format = jsonencode({
      type = "object"
      properties = {
        capital = { type = "string" }
      }
      required             = ["capital"]
      additionalProperties = false
    })
  }

  messages = [
    {
      role    = "user"
      content = "What is the capital of France?"
    }
  ]
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrWith("anthropic_message.test_structured", "content", func(v string) error {
						var out struct {
							Capital string `json:"capital"`
						}
						if err := json.Unmarshal([]byte(v), &out); err != nil {
							return fmt.Errorf("content is not valid JSON: %w (%q)", err, v)
						}
						if out.Capital == "" {
							return fmt.Errorf("capital missing in %q", v)
						}
						return nil
					}),
					resource.TestCheckResourceAttr("anthropic_message.test_structured", "thinking_tokens", "0"),
				),
			},
		},
	})
}
