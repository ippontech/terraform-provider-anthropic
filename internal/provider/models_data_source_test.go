// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccModelsDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccModelsDataSourceConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.anthropic_models.test", "models.#"),
					resource.TestCheckResourceAttrSet("data.anthropic_models.test", "models.0.id"),
					resource.TestCheckResourceAttrSet("data.anthropic_models.test", "models.0.display_name"),
					resource.TestCheckResourceAttrSet("data.anthropic_models.test", "models.0.created_at"),
					resource.TestCheckResourceAttrSet("data.anthropic_models.test", "models.0.max_input_tokens"),
					resource.TestCheckResourceAttrSet("data.anthropic_models.test", "models.0.max_tokens"),
					resource.TestCheckResourceAttrSet("data.anthropic_models.test", "models.0.capabilities.batch"),
				),
			},
		},
	})
}

const testAccModelsDataSourceConfig = `
data "anthropic_models" "test" {}
`
