// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccModelDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccModelDataSourceConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.anthropic_model.test", "model_id", "claude-haiku-4-5-20251001"),
					resource.TestCheckResourceAttrSet("data.anthropic_model.test", "id"),
					resource.TestCheckResourceAttrSet("data.anthropic_model.test", "display_name"),
					resource.TestCheckResourceAttrSet("data.anthropic_model.test", "created_at"),
					resource.TestCheckResourceAttrSet("data.anthropic_model.test", "max_input_tokens"),
					resource.TestCheckResourceAttrSet("data.anthropic_model.test", "max_tokens"),
					resource.TestCheckResourceAttrSet("data.anthropic_model.test", "capabilities.batch"),
					resource.TestCheckResourceAttrSet("data.anthropic_model.test", "capabilities.citations"),
					resource.TestCheckResourceAttrSet("data.anthropic_model.test", "capabilities.code_execution"),
					resource.TestCheckResourceAttrSet("data.anthropic_model.test", "capabilities.image_input"),
					resource.TestCheckResourceAttrSet("data.anthropic_model.test", "capabilities.pdf_input"),
					resource.TestCheckResourceAttrSet("data.anthropic_model.test", "capabilities.structured_outputs"),
					resource.TestCheckResourceAttrSet("data.anthropic_model.test", "capabilities.thinking"),
					resource.TestCheckResourceAttrSet("data.anthropic_model.test", "capabilities.context_management.compact_20260112"),
					resource.TestCheckResourceAttrSet("data.anthropic_model.test", "capabilities.effort.high"),
					resource.TestCheckResourceAttrSet("data.anthropic_model.test", "capabilities.effort.low"),
				),
			},
		},
	})
}

const testAccModelDataSourceConfig = `
data "anthropic_model" "test" {
  model_id = "claude-haiku-4-5-20251001"
}
`
