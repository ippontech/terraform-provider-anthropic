// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package skills_test

import (
	"fmt"
	"testing"

	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccSkillVersionDataSource_basic(t *testing.T) {
	skillFilePath := testAccSkillVersionFilePath(t)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckSkillDestroyed,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(testAccSkillVersionDataSourceBasicConfig, skillFilePath, skillFilePath),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.anthropic_skill_version.test", "id"),
					resource.TestCheckResourceAttrSet("data.anthropic_skill_version.test", "created_at"),
					resource.TestCheckResourceAttr("data.anthropic_skill_version.test", "type", "skill_version"),
					resource.TestCheckResourceAttrPair(
						"data.anthropic_skill_version.test", "skill_id",
						"anthropic_skill_version.test", "skill_id",
					),
					resource.TestCheckResourceAttrPair(
						"data.anthropic_skill_version.test", "version",
						"anthropic_skill_version.test", "version",
					),
				),
			},
		},
	})
}

const testAccSkillVersionDataSourceBasicConfig = `
resource "anthropic_skill" "test" {
  files         = [%q]
  force_destroy = true
}
resource "anthropic_skill_version" "test" {
  skill_id = anthropic_skill.test.id
  files    = [%q]
}
data "anthropic_skill_version" "test" {
  skill_id = anthropic_skill_version.test.skill_id
  version  = anthropic_skill_version.test.version
}
`
