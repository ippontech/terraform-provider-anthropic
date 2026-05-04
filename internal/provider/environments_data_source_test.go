// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccEnvironmentsDataSource_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccEnvironmentsDataSourceBasicConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.anthropic_environments.test", "environments.#"),
				),
			},
		},
	})
}

const testAccEnvironmentsDataSourceBasicConfig = `
data "anthropic_environments" "test" {}
`

func TestAccEnvironmentsDataSource_withEnvironments(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckEnvironmentDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccEnvironmentsDataSourceWithEnvironmentsConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.anthropic_environments.test", "environments.#"),
					resource.TestCheckTypeSetElemNestedAttrs("data.anthropic_environments.test", "environments.*", map[string]string{
						"name": "tf-acc-test-envs-ds-one",
						"type": "environment",
					}),
					resource.TestCheckTypeSetElemNestedAttrs("data.anthropic_environments.test", "environments.*", map[string]string{
						"name": "tf-acc-test-envs-ds-two",
						"type": "environment",
					}),
				),
			},
		},
	})
}

const testAccEnvironmentsDataSourceWithEnvironmentsConfig = `
resource "anthropic_environment" "one" {
  name = "tf-acc-test-envs-ds-one"
}

resource "anthropic_environment" "two" {
  name = "tf-acc-test-envs-ds-two"
}

data "anthropic_environments" "test" {
  depends_on = [anthropic_environment.one, anthropic_environment.two]
}
`
