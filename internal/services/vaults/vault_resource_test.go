// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package vaults_test

import (
	"testing"

	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// Vaults are a standard-API (non-admin) resource scoped to the workspace of the
// ANTHROPIC_API_KEY used by the test, so they support full create/read/update/
// delete acceptance tests (unlike the admin-API resources blocked by #58).
// Vaults are billed only at runtime, so creating and destroying them in tests is
// free; CheckDestroy guarantees no dangling resources remain.
//
// The destroy/archive checks poll via the shared helpers in helpers_test.go to
// ride out the vaults API's read-after-write staleness window.

func testAccCheckVaultDestroyed(s *terraform.State) error {
	client := newAccTestClient()
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "anthropic_vault" {
			continue
		}
		if err := awaitVaultGone(client, rs.Primary.ID); err != nil {
			return err
		}
	}
	return nil
}

// testAccCheckVaultArchivedAndCleanup verifies the vault was archived (not
// hard-deleted) and then permanently deletes it to avoid dangling resources.
func testAccCheckVaultArchivedAndCleanup(s *terraform.State) error {
	client := newAccTestClient()
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "anthropic_vault" {
			continue
		}
		if err := awaitVaultArchived(client, rs.Primary.ID); err != nil {
			return err
		}
		if err := hardDeleteVault(client, rs.Primary.ID); err != nil {
			return err
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
