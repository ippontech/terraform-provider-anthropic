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

func testAccSkillFilePath(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "SKILL.md")
	if err := os.WriteFile(p, []byte("---\nname: test-skill\ndescription: For testing.\n---\n\n# Test Skill\n\nFor testing.\n"), 0644); err != nil {
		t.Fatalf("failed to write test skill file: %s", err)
	}
	return p
}

func testAccCheckSkillDestroyed(s *terraform.State) error {
	client := anthropic.NewClient(option.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")))
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "anthropic_skill" {
			continue
		}
		_, err := client.Beta.Skills.Get(context.Background(), rs.Primary.ID, anthropic.BetaSkillGetParams{})
		if err != nil {
			// Resource not found — destroyed successfully.
			return nil
		}
		return fmt.Errorf("skill %s still exists", rs.Primary.ID)
	}
	return nil
}

func TestAccSkillResource_basic(t *testing.T) {
	skillFilePath := testAccSkillFilePath(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckSkillDestroyed,
		Steps: []resource.TestStep{
			// Create and Read
			{
				Config: fmt.Sprintf(testAccSkillResourceBasicConfig, skillFilePath),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_skill.test", "id"),
					resource.TestCheckResourceAttr("anthropic_skill.test", "source", "custom"),
					resource.TestCheckResourceAttrSet("anthropic_skill.test", "created_at"),
					resource.TestCheckResourceAttrSet("anthropic_skill.test", "updated_at"),
					resource.TestCheckResourceAttrSet("anthropic_skill.test", "latest_version"),
				),
			},
			// ImportState — display_title is not set in config, so the API may return a
			// server-derived value that won't match after import.
			{
				ResourceName:            "anthropic_skill.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"files", "display_title"},
			},
		},
	})
}

func TestAccSkillResource_withDisplayTitle(t *testing.T) {
	skillFilePath := testAccSkillFilePath(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckSkillDestroyed,
		Steps: []resource.TestStep{
			// Create and Read
			{
				Config: fmt.Sprintf(testAccSkillResourceWithDisplayTitleConfig, skillFilePath),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_skill.test", "id"),
					resource.TestCheckResourceAttr("anthropic_skill.test", "display_title", "tf-acc-test-skill"),
					resource.TestCheckResourceAttr("anthropic_skill.test", "source", "custom"),
				),
			},
			// ImportState
			{
				ResourceName:            "anthropic_skill.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"files"},
			},
		},
	})
}

const testAccSkillResourceBasicConfig = `
resource "anthropic_skill" "test" {
  files = [%q]
}
`

const testAccSkillResourceWithDisplayTitleConfig = `
resource "anthropic_skill" "test" {
  display_title = "tf-acc-test-skill"
  files         = [%q]
}
`
