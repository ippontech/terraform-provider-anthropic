// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccSkillVersionsDataSource_basic(t *testing.T) {
	skillFilePath := testAccSkillVersionFilePath(t)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckSkillDestroyed,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(testAccSkillVersionsDataSourceBasicConfig, skillFilePath, skillFilePath),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.anthropic_skill_versions.test", "versions.#"),
					resource.TestCheckResourceAttrSet("data.anthropic_skill_versions.test", "versions.0.id"),
					resource.TestCheckResourceAttrSet("data.anthropic_skill_versions.test", "versions.0.version"),
					resource.TestCheckResourceAttrSet("data.anthropic_skill_versions.test", "versions.0.created_at"),
					resource.TestCheckResourceAttr("data.anthropic_skill_versions.test", "versions.0.type", "skill_version"),
				),
			},
		},
	})
}

const testAccSkillVersionsDataSourceBasicConfig = `
resource "anthropic_skill" "test" {
  files = [%q]
}
resource "anthropic_skill_version" "test" {
  skill_id = anthropic_skill.test.id
  files    = [%q]
}
data "anthropic_skill_versions" "test" {
  skill_id   = anthropic_skill.test.id
  depends_on = [anthropic_skill_version.test]
}
`
