// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccEnvironmentDataSource_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccEnvironmentDataSourceBasicConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.anthropic_environment.test", "id"),
					resource.TestCheckResourceAttr("data.anthropic_environment.test", "name", "tf-acc-test-env-ds-basic"),
					resource.TestCheckResourceAttr("data.anthropic_environment.test", "type", "environment"),
					resource.TestCheckResourceAttrSet("data.anthropic_environment.test", "created_at"),
					resource.TestCheckResourceAttrSet("data.anthropic_environment.test", "updated_at"),
					resource.TestCheckResourceAttrPair(
						"data.anthropic_environment.test", "id",
						"anthropic_environment.test", "id",
					),
				),
			},
		},
	})
}

const testAccEnvironmentDataSourceBasicConfig = `
resource "anthropic_environment" "test" {
  name = "tf-acc-test-env-ds-basic"
}

data "anthropic_environment" "test" {
  environment_id = anthropic_environment.test.id
}
`
