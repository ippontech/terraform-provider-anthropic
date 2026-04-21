// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func testAccSkillVersionFilePath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(p, []byte("# Test Skill Version\n\nFor testing.\n"), 0644); err != nil {
		t.Fatalf("failed to write test skill version file: %s", err)
	}
	return p
}

func testAccCheckSkillVersionDestroyed(s *terraform.State) error {
	client := anthropic.NewClient(option.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")))
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "anthropic_skill_version" {
			continue
		}
		skillID := rs.Primary.Attributes["skill_id"]
		version := rs.Primary.Attributes["version"]
		_, err := client.Beta.Skills.Versions.Get(context.Background(), version, anthropic.BetaSkillVersionGetParams{
			SkillID: skillID,
		})
		if err != nil {
			// Resource not found — destroyed successfully.
			return nil
		}
		return fmt.Errorf("skill version %s/%s still exists", skillID, version)
	}
	return nil
}

func TestAccSkillVersionResource_basic(t *testing.T) {
	skillFilePath := testAccSkillVersionFilePath(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckSkillVersionDestroyed,
		Steps: []resource.TestStep{
			// Create and Read
			{
				Config: fmt.Sprintf(testAccSkillVersionResourceBasicConfig, skillFilePath, skillFilePath),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_skill_version.test", "id"),
					resource.TestCheckResourceAttrSet("anthropic_skill_version.test", "version"),
					resource.TestCheckResourceAttrSet("anthropic_skill_version.test", "skill_id"),
					resource.TestCheckResourceAttrSet("anthropic_skill_version.test", "created_at"),
				),
			},
			// ImportState
			{
				ResourceName:            "anthropic_skill_version.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"files"},
				ImportStateIdFunc: func(s *terraform.State) (string, error) {
					rs := s.RootModule().Resources["anthropic_skill_version.test"]
					if rs == nil {
						return "", fmt.Errorf("resource not found in state")
					}
					return rs.Primary.Attributes["skill_id"] + "/" + rs.Primary.Attributes["version"], nil
				},
			},
		},
	})
}

const testAccSkillVersionResourceBasicConfig = `
resource "anthropic_skill" "test" {
  files = [%q]
}

resource "anthropic_skill_version" "test" {
  skill_id = anthropic_skill.test.id
  files    = [%q]
}
`
