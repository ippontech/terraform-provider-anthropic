// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package vaults_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"testing"
	"time"

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

// The vaults API serves stale reads for up to ~1s after a write (see the
// read-after-write note in CLAUDE.md and #193): a Get issued right after the
// post-test destroy can still return the deleted — or not-yet-archived —
// object, which made CheckDestroy flaky in CI. The destroy/archive checks
// below therefore poll until the API converges, reusing the ceiling and
// interval of awaitVaultUpdateVisible in the resource itself.
const (
	destroyCheckTimeout  = 5 * time.Second
	destroyCheckInterval = 200 * time.Millisecond
)

// isNotFoundError reports whether err is an API 404.
func isNotFoundError(err error) bool {
	var apiErr *anthropic.Error
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

// awaitGone polls get until it returns 404 (destroyed) or the deadline passes.
// Any other error is returned as-is: it says nothing about visibility, and
// treating it as "destroyed" would let an auth failure pass the check.
func awaitGone(kind, id string, get func(ctx context.Context) error) error {
	deadline := time.Now().Add(destroyCheckTimeout)
	for {
		err := get(context.Background())
		if isNotFoundError(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("checking %s %s after destroy: %w", kind, id, err)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s %s still exists %v after destroy", kind, id, destroyCheckTimeout)
		}
		time.Sleep(destroyCheckInterval)
	}
}

// awaitArchived polls get until it returns a non-zero archived_at, guarding
// against the same staleness window on the archive write. A read error fails
// immediately: the object existed before the archive, so a stale read shows
// the unarchived object, never a 404.
func awaitArchived(kind, id string, get func(ctx context.Context) (time.Time, error)) error {
	deadline := time.Now().Add(destroyCheckTimeout)
	for {
		archivedAt, err := get(context.Background())
		if err != nil {
			return fmt.Errorf("%s %s not found after archive destroy: %w", kind, id, err)
		}
		if !archivedAt.IsZero() {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("%s %s was not archived on destroy", kind, id)
		}
		time.Sleep(destroyCheckInterval)
	}
}

func testAccCheckVaultDestroyed(s *terraform.State) error {
	client := anthropic.NewClient(option.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")))
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "anthropic_vault" {
			continue
		}
		err := awaitGone("vault", rs.Primary.ID, func(ctx context.Context) error {
			_, err := client.Beta.Vaults.Get(ctx, rs.Primary.ID, anthropic.BetaVaultGetParams{})
			return err
		})
		if err != nil {
			return err
		}
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
		err := awaitArchived("vault", rs.Primary.ID, func(ctx context.Context) (time.Time, error) {
			vault, err := client.Beta.Vaults.Get(ctx, rs.Primary.ID, anthropic.BetaVaultGetParams{})
			if err != nil {
				return time.Time{}, err
			}
			return vault.ArchivedAt, nil
		})
		if err != nil {
			return err
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
