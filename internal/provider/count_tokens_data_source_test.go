// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccCountTokensDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccCountTokensDataSourceConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.anthropic_count_tokens.test", "model", "claude-haiku-4-5-20251001"),
					resource.TestCheckResourceAttr("data.anthropic_count_tokens.test", "messages.0.role", "user"),
					resource.TestCheckResourceAttr("data.anthropic_count_tokens.test", "messages.0.content", "Hello, Claude"),
					resource.TestCheckResourceAttrWith("data.anthropic_count_tokens.test", "input_tokens", func(v string) error {
						n, err := strconv.ParseInt(v, 10, 64)
						if err != nil {
							return err
						}
						if n <= 0 {
							return fmt.Errorf("expected input_tokens > 0, got %d", n)
						}
						return nil
					}),
				),
			},
		},
	})
}

func TestAccCountTokensDataSourceWithSystem(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccCountTokensDataSourceWithSystemConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.anthropic_count_tokens.test_system", "input_tokens"),
					resource.TestCheckResourceAttr("data.anthropic_count_tokens.test_system", "system", "You are a helpful assistant."),
				),
			},
		},
	})
}

const testAccCountTokensDataSourceConfig = `
data "anthropic_count_tokens" "test" {
  model = "claude-haiku-4-5-20251001"

  messages = [
    {
      role    = "user"
      content = "Hello, Claude"
    }
  ]
}
`

const testAccCountTokensDataSourceWithSystemConfig = `
data "anthropic_count_tokens" "test_system" {
  model  = "claude-haiku-4-5-20251001"
  system = "You are a helpful assistant."

  messages = [
    {
      role    = "user"
      content = "Hello, Claude"
    }
  ]
}
`
