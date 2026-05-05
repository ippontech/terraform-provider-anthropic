// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package skills_test

import (
	"fmt"
	"testing"

	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func TestAccSkillDataSource_basic(t *testing.T) {
	skillFilePath := testAccSkillFilePath(t)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(testAccSkillDataSourceBasicConfig, skillFilePath),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.anthropic_skill.test", "id"),
					resource.TestCheckResourceAttr("data.anthropic_skill.test", "source", "custom"),
					resource.TestCheckResourceAttrPair(
						"data.anthropic_skill.test", "id",
						"anthropic_skill.test", "id",
					),
				),
			},
		},
	})
}

const testAccSkillDataSourceBasicConfig = `
resource "anthropic_skill" "test" {
  files         = [%q]
  force_destroy = true
}
data "anthropic_skill" "test" {
  skill_id = anthropic_skill.test.id
}
`
