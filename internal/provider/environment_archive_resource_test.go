// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccEnvironmentArchiveResource_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccEnvironmentArchiveResourceBasicConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_environment_archive.test", "id"),
					resource.TestCheckResourceAttrSet("anthropic_environment_archive.test", "archived_at"),
					resource.TestCheckResourceAttrPair(
						"anthropic_environment_archive.test", "id",
						"anthropic_environment.test", "id",
					),
				),
			},
			{
				ResourceName:      "anthropic_environment_archive.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

const testAccEnvironmentArchiveResourceBasicConfig = `
resource "anthropic_environment" "test" {
  name = "tf-acc-test-env-archive"
}

resource "anthropic_environment_archive" "test" {
  environment_id = anthropic_environment.test.id
}
`
