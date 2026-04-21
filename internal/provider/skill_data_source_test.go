// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

func testAccSkillDataSourceFilePath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(p, []byte("# Test Skill\n\nFor testing.\n"), 0644); err != nil {
		t.Fatalf("failed to write test skill file: %s", err)
	}
	return p
}

func TestAccSkillDataSource_basic(t *testing.T) {
	skillFilePath := testAccSkillDataSourceFilePath(t)
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
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
  files = [%q]
}
data "anthropic_skill" "test" {
  skill_id = anthropic_skill.test.id
}
`
