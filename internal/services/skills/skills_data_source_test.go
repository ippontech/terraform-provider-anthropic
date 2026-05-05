// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package skills_test

import (
	"fmt"
	"testing"

	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccSkillsDataSource_basic(t *testing.T) {
	skillFilePath := testAccSkillFilePath(t)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckSkillDestroyed,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(testAccSkillsDataSourceBasicConfig, skillFilePath),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.anthropic_skills.test", "skills.#"),
				),
			},
		},
	})
}

func TestAccSkillsDataSource_sourceFilter(t *testing.T) {
	skillFilePath := testAccSkillFilePath(t)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckSkillDestroyed,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(testAccSkillsDataSourceWithFilterConfig, skillFilePath),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.anthropic_skills.filtered", "skills.#"),
					resource.TestCheckResourceAttr("data.anthropic_skills.filtered", "skills.0.source", "custom"),
				),
			},
		},
	})
}

const testAccSkillsDataSourceBasicConfig = `
resource "anthropic_skill" "test" {
  files         = [%q]
  force_destroy = true
}
data "anthropic_skills" "test" {
  depends_on = [anthropic_skill.test]
}
`

const testAccSkillsDataSourceWithFilterConfig = `
resource "anthropic_skill" "test" {
  files         = [%q]
  force_destroy = true
}
data "anthropic_skills" "filtered" {
  source_filter = "custom"
  depends_on    = [anthropic_skill.test]
}
`
