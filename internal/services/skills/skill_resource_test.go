// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package skills_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func testAccSkillFilePath(t *testing.T) string {
	t.Helper()
	// The API requires the multipart folder name to match the `name` field in
	// the SKILL.md frontmatter. Create an explicit subdirectory so dirName is
	// predictable rather than relying on t.TempDir()'s random suffix.
	dir := filepath.Join(t.TempDir(), "test-skill")
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatalf("failed to create test skill directory: %s", err)
	}
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
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
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
			// ImportState — display_title is not set in config so the API may return a
			// server-derived value; force_destroy is not tracked server-side;
			// updated_at may differ between the create response and a subsequent GET.
			{
				ResourceName:            "anthropic_skill.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"files", "display_title", "force_destroy", "updated_at"},
			},
		},
	})
}

func TestAccSkillResource_withDisplayTitle(t *testing.T) {
	skillFilePath := testAccSkillFilePath(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
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
			// ImportState — force_destroy is not tracked server-side;
			// updated_at may differ between create response and a subsequent GET.
			{
				ResourceName:            "anthropic_skill.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"files", "force_destroy", "updated_at"},
			},
		},
	})
}

const testAccSkillResourceBasicConfig = `
resource "anthropic_skill" "test" {
  files         = [%q]
  force_destroy = true
}
`

const testAccSkillResourceWithDisplayTitleConfig = `
resource "anthropic_skill" "test" {
  display_title = "tf-acc-test-skill"
  files         = [%q]
  force_destroy = true
}
`
