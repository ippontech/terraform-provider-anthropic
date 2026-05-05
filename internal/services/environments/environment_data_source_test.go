// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package environments_test

import (
	"testing"

	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccEnvironmentDataSource_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
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
