// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package vaults_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// Vaults are a standard-API (non-admin) resource scoped to the workspace of the
// ANTHROPIC_API_KEY used by the test, so they support full create/read/update/
// delete acceptance tests (unlike the admin-API resources blocked by #58).
// Vaults are billed only at runtime, so creating and destroying them in tests is
// free; CheckDestroy guarantees no dangling resources remain.

func testAccCheckVaultDestroyed(s *terraform.State) error {
	client := anthropic.NewClient(option.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")))
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "anthropic_vault" {
			continue
		}
		_, err := client.Beta.Vaults.Get(context.Background(), rs.Primary.ID, anthropic.BetaVaultGetParams{})
		if err != nil {
			// Resource not found — destroyed successfully.
			return nil
		}
		return fmt.Errorf("vault %s still exists", rs.Primary.ID)
	}
	return nil
}

// testAccCheckVaultArchivedAndCleanup verifies the vault was archived (not
// hard-deleted) and then permanently deletes it to avoid dangling resources.
func testAccCheckVaultArchivedAndCleanup(s *terraform.State) error {
	client := anthropic.NewClient(option.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")))
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "anthropic_vault" {
			continue
		}
		vault, err := client.Beta.Vaults.Get(context.Background(), rs.Primary.ID, anthropic.BetaVaultGetParams{})
		if err != nil {
			return fmt.Errorf("vault %s not found after archive destroy: %w", rs.Primary.ID, err)
		}
		if vault.ArchivedAt.IsZero() {
			return fmt.Errorf("vault %s was not archived on destroy", rs.Primary.ID)
		}
		// Hard-delete the archived vault so it doesn't accumulate in the workspace.
		if _, err := client.Beta.Vaults.Delete(context.Background(), rs.Primary.ID, anthropic.BetaVaultDeleteParams{}); err != nil {
			return fmt.Errorf("cleanup: unable to delete archived vault %s: %w", rs.Primary.ID, err)
		}
	}
	return nil
}

const testAccVaultResourceBasicConfig = `
resource "anthropic_vault" "test" {
  display_name = "tf-acc-test-vault-basic"
}
`

const testAccVaultResourceWithMetadataConfig = `
resource "anthropic_vault" "test" {
  display_name = "tf-acc-test-vault-metadata"

  metadata = {
    team = "terraform"
    env  = "test"
  }
}
`

func TestAccVaultResource_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckVaultDestroyed,
		Steps: []resource.TestStep{
			// Create and Read
			{
				Config: testAccVaultResourceBasicConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_vault.test", "id"),
					resource.TestCheckResourceAttr("anthropic_vault.test", "display_name", "tf-acc-test-vault-basic"),
					resource.TestCheckResourceAttr("anthropic_vault.test", "type", "vault"),
					resource.TestCheckResourceAttrSet("anthropic_vault.test", "created_at"),
					resource.TestCheckResourceAttrSet("anthropic_vault.test", "updated_at"),
					resource.TestCheckResourceAttr("anthropic_vault.test", "archive_on_destroy", "false"),
				),
			},
			// ImportState
			{
				ResourceName:            "anthropic_vault.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"archive_on_destroy"},
			},
		},
	})
}

func TestAccVaultResource_withMetadata(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckVaultDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccVaultResourceWithMetadataConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_vault.test", "id"),
					resource.TestCheckResourceAttr("anthropic_vault.test", "display_name", "tf-acc-test-vault-metadata"),
					resource.TestCheckResourceAttr("anthropic_vault.test", "metadata.team", "terraform"),
					resource.TestCheckResourceAttr("anthropic_vault.test", "metadata.env", "test"),
				),
			},
			{
				ResourceName:            "anthropic_vault.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"archive_on_destroy"},
			},
		},
	})
}

const testAccVaultResourceUpdateConfigV1 = `
resource "anthropic_vault" "test" {
  display_name = "tf-acc-test-vault-update-v1"

  metadata = {
    team = "terraform"
    env  = "test"
  }
}
`

// V2 renames the vault, changes one metadata value, and removes another key —
// exercising the PATCH-with-null clear path in buildMetadataPatch.
const testAccVaultResourceUpdateConfigV2 = `
resource "anthropic_vault" "test" {
  display_name = "tf-acc-test-vault-update-v2"

  metadata = {
    team = "platform"
  }
}
`

func TestAccVaultResource_update(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckVaultDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccVaultResourceUpdateConfigV1,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("anthropic_vault.test", "display_name", "tf-acc-test-vault-update-v1"),
					resource.TestCheckResourceAttr("anthropic_vault.test", "metadata.team", "terraform"),
					resource.TestCheckResourceAttr("anthropic_vault.test", "metadata.env", "test"),
				),
			},
			{
				Config: testAccVaultResourceUpdateConfigV2,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("anthropic_vault.test", "display_name", "tf-acc-test-vault-update-v2"),
					resource.TestCheckResourceAttr("anthropic_vault.test", "metadata.team", "platform"),
					// env was removed in config and must be cleared server-side.
					resource.TestCheckNoResourceAttr("anthropic_vault.test", "metadata.env"),
				),
			},
		},
	})
}

const testAccVaultResourceArchiveOnDestroyConfig = `
resource "anthropic_vault" "test" {
  display_name       = "tf-acc-test-vault-archive-on-destroy"
  archive_on_destroy = true
}
`

func TestAccVaultResource_archiveOnDestroy(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckVaultArchivedAndCleanup,
		Steps: []resource.TestStep{
			{
				Config: testAccVaultResourceArchiveOnDestroyConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_vault.test", "id"),
					resource.TestCheckResourceAttr("anthropic_vault.test", "display_name", "tf-acc-test-vault-archive-on-destroy"),
					resource.TestCheckResourceAttr("anthropic_vault.test", "archive_on_destroy", "true"),
				),
			},
			// ImportState — archive_on_destroy is local-only; provider defaults it to false on import.
			{
				ResourceName:            "anthropic_vault.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"archive_on_destroy"},
			},
		},
	})
}
