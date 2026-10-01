// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package memorystores_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

var regexpMustCompileArchived = regexp.MustCompile(`(?s)archived`)

// Memory stores are a standard-API (non-admin) resource scoped to the
// workspace of the ANTHROPIC_API_KEY used by the test, so they support full
// create/read/update/delete acceptance tests (unlike the admin-API resources
// blocked by #58). They are cheap to create and destroy.

const (
	destroyCheckTimeout  = 5 * time.Second
	destroyCheckInterval = 200 * time.Millisecond
)

func newAccTestClient() anthropic.Client {
	return *acctest.NewAPIKeyClient()
}

func isNotFoundError(err error) bool {
	var apiErr *anthropic.Error
	return errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound
}

// awaitMemoryStoreGone polls Get until it returns 404 (destroyed) or the
// deadline passes. A transient non-404 error is retried rather than failing
// the check immediately, mirroring the vaults acceptance test helpers.
func awaitMemoryStoreGone(client anthropic.Client, id string) error {
	deadline := time.Now().Add(destroyCheckTimeout)
	var lastErr error
	for {
		_, err := client.Beta.MemoryStores.Get(context.Background(), id, anthropic.BetaMemoryStoreGetParams{})
		if isNotFoundError(err) {
			return nil
		}
		if err == nil {
			lastErr = fmt.Errorf("memory store %s still exists %v after destroy", id, destroyCheckTimeout)
		} else {
			lastErr = fmt.Errorf("checking memory store %s after destroy: %w", id, err)
		}
		if time.Now().After(deadline) {
			return lastErr
		}
		time.Sleep(destroyCheckInterval)
	}
}

// awaitMemoryStoreArchived polls Get until it returns a non-zero archived_at.
func awaitMemoryStoreArchived(client anthropic.Client, id string) error {
	deadline := time.Now().Add(destroyCheckTimeout)
	for {
		store, err := client.Beta.MemoryStores.Get(context.Background(), id, anthropic.BetaMemoryStoreGetParams{})
		if err != nil {
			return fmt.Errorf("memory store %s not found after archive destroy: %w", id, err)
		}
		if !store.ArchivedAt.IsZero() {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("memory store %s was not archived on destroy", id)
		}
		time.Sleep(destroyCheckInterval)
	}
}

// hardDeleteMemoryStore permanently deletes an archived store so it doesn't
// accumulate in the test workspace.
func hardDeleteMemoryStore(client anthropic.Client, id string) error {
	if _, err := client.Beta.MemoryStores.Delete(context.Background(), id, anthropic.BetaMemoryStoreDeleteParams{}); err != nil {
		return fmt.Errorf("cleanup: unable to delete archived memory store %s: %w", id, err)
	}
	return nil
}

func testAccCheckMemoryStoreDestroyed(s *terraform.State) error {
	client := newAccTestClient()
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "anthropic_memory_store" {
			continue
		}
		if err := awaitMemoryStoreGone(client, rs.Primary.ID); err != nil {
			return err
		}
	}
	return nil
}

// testAccCheckMemoryStoreArchivedAndCleanup verifies the store was archived
// (not hard-deleted) and then permanently deletes it to avoid dangling
// resources in the test workspace.
func testAccCheckMemoryStoreArchivedAndCleanup(s *terraform.State) error {
	client := newAccTestClient()
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "anthropic_memory_store" {
			continue
		}
		if err := awaitMemoryStoreArchived(client, rs.Primary.ID); err != nil {
			return err
		}
		if err := hardDeleteMemoryStore(client, rs.Primary.ID); err != nil {
			return err
		}
	}
	return nil
}

const testAccMemoryStoreResourceBasicConfig = `
resource "anthropic_memory_store" "test" {
  name        = "tf-acc-test-memstore-basic"
  description = "tf acceptance test store"
}
`

func TestAccMemoryStoreResource_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckMemoryStoreDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccMemoryStoreResourceBasicConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_memory_store.test", "id"),
					resource.TestCheckResourceAttr("anthropic_memory_store.test", "name", "tf-acc-test-memstore-basic"),
					resource.TestCheckResourceAttr("anthropic_memory_store.test", "description", "tf acceptance test store"),
					resource.TestCheckResourceAttrSet("anthropic_memory_store.test", "created_at"),
					resource.TestCheckNoResourceAttr("anthropic_memory_store.test", "archived_at"),
					resource.TestCheckResourceAttr("anthropic_memory_store.test", "archive_on_destroy", "false"),
				),
			},
			{
				ResourceName:            "anthropic_memory_store.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"archive_on_destroy"},
			},
		},
	})
}

const testAccMemoryStoreResourceUpdateConfigV1 = `
resource "anthropic_memory_store" "test" {
  name        = "tf-acc-test-memstore-update-v1"
  description = "version 1"

  metadata = {
    team = "terraform"
    env  = "test"
  }
}
`

// V2 renames the store, changes the description, changes one metadata value,
// and removes another key — exercising the PATCH-with-null clear path in
// buildMemoryStoreMetadataPatch.
const testAccMemoryStoreResourceUpdateConfigV2 = `
resource "anthropic_memory_store" "test" {
  name        = "tf-acc-test-memstore-update-v2"
  description = "version 2"

  metadata = {
    team = "platform"
  }
}
`

func TestAccMemoryStoreResource_update(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckMemoryStoreDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccMemoryStoreResourceUpdateConfigV1,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("anthropic_memory_store.test", "name", "tf-acc-test-memstore-update-v1"),
					resource.TestCheckResourceAttr("anthropic_memory_store.test", "description", "version 1"),
					resource.TestCheckResourceAttr("anthropic_memory_store.test", "metadata.team", "terraform"),
					resource.TestCheckResourceAttr("anthropic_memory_store.test", "metadata.env", "test"),
				),
			},
			{
				Config: testAccMemoryStoreResourceUpdateConfigV2,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("anthropic_memory_store.test", "name", "tf-acc-test-memstore-update-v2"),
					resource.TestCheckResourceAttr("anthropic_memory_store.test", "description", "version 2"),
					resource.TestCheckResourceAttr("anthropic_memory_store.test", "metadata.team", "platform"),
					// env was removed in config and must be cleared server-side.
					resource.TestCheckNoResourceAttr("anthropic_memory_store.test", "metadata.env"),
				),
			},
		},
	})
}

const testAccMemoryStoreResourceArchiveOnDestroyConfig = `
resource "anthropic_memory_store" "test" {
  name                = "tf-acc-test-memstore-archive-on-destroy"
  description         = "archived instead of deleted"
  archive_on_destroy  = true
}
`

func TestAccMemoryStoreResource_archiveOnDestroy(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckMemoryStoreArchivedAndCleanup,
		Steps: []resource.TestStep{
			{
				Config: testAccMemoryStoreResourceArchiveOnDestroyConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_memory_store.test", "id"),
					resource.TestCheckResourceAttr("anthropic_memory_store.test", "name", "tf-acc-test-memstore-archive-on-destroy"),
					resource.TestCheckResourceAttr("anthropic_memory_store.test", "archive_on_destroy", "true"),
				),
			},
			// ImportState — archive_on_destroy is local-only; provider defaults it to false on import.
			{
				ResourceName:            "anthropic_memory_store.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"archive_on_destroy"},
			},
		},
	})
}

// TestAccMemoryStoreResource_archivedUpdateRejected creates a store, archives
// it out-of-band (the API has no unarchive), then attempts a config change on
// name and asserts Update fails with a clear error rather than a confusing
// API 4xx. The resource's own Delete (archive_on_destroy defaults to false)
// already hard-deletes the store on the test framework's final destroy, so
// CheckDestroy only verifies that — it must not also try to delete it, or the
// second DELETE 404s.
func TestAccMemoryStoreResource_archivedUpdateRejected(t *testing.T) {
	client := newAccTestClient()
	var storeID string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy: func(s *terraform.State) error {
			if storeID == "" {
				return nil
			}
			return awaitMemoryStoreGone(client, storeID)
		},
		Steps: []resource.TestStep{
			{
				Config: `
resource "anthropic_memory_store" "test" {
  name        = "tf-acc-test-memstore-archived-update"
  description = "will be archived out-of-band"
}
`,
				Check: resource.ComposeAggregateTestCheckFunc(
					func(s *terraform.State) error {
						rs, ok := s.RootModule().Resources["anthropic_memory_store.test"]
						if !ok {
							return fmt.Errorf("resource not found in state")
						}
						storeID = rs.Primary.ID

						if _, err := client.Beta.MemoryStores.Archive(context.Background(), storeID, anthropic.BetaMemoryStoreArchiveParams{}); err != nil {
							return fmt.Errorf("unable to archive memory store out-of-band: %w", err)
						}
						return awaitMemoryStoreArchived(client, storeID)
					},
				),
			},
			{
				Config: `
resource "anthropic_memory_store" "test" {
  name        = "tf-acc-test-memstore-archived-update-renamed"
  description = "will be archived out-of-band"
}
`,
				ExpectError: regexpMustCompileArchived,
			},
		},
	})
}
