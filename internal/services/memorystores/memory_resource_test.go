// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package memorystores_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

// Memories are, like their parent memory stores, a standard-API resource
// scoped to the workspace of the ANTHROPIC_API_KEY used by the test, so they
// support full create/read/update/delete acceptance tests. Memories only
// support hard delete (no archive_on_destroy), so cleanup is a single
// Delete call.

// awaitMemoryGone polls Get until it returns 404 (destroyed) or the deadline
// passes, mirroring awaitMemoryStoreGone.
func awaitMemoryGone(client anthropic.Client, memoryStoreID, memoryID string) error {
	deadline := time.Now().Add(destroyCheckTimeout)
	var lastErr error
	for {
		_, err := client.Beta.MemoryStores.Memories.Get(context.Background(), memoryID, anthropic.BetaMemoryStoreMemoryGetParams{
			MemoryStoreID: memoryStoreID,
		})
		if isNotFoundError(err) {
			return nil
		}
		if err == nil {
			lastErr = fmt.Errorf("memory %s still exists %v after destroy", memoryID, destroyCheckTimeout)
		} else {
			lastErr = fmt.Errorf("checking memory %s after destroy: %w", memoryID, err)
		}
		if time.Now().After(deadline) {
			return lastErr
		}
		time.Sleep(destroyCheckInterval)
	}
}

func testAccCheckMemoryDestroyed(s *terraform.State) error {
	client := newAccTestClient()
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "anthropic_memory" {
			continue
		}
		memoryStoreID := rs.Primary.Attributes["memory_store_id"]
		if err := awaitMemoryGone(client, memoryStoreID, rs.Primary.ID); err != nil {
			return err
		}
	}
	return nil
}

const testAccMemoryResourceBasicConfig = `
resource "anthropic_memory_store" "test" {
  name        = "tf-acc-test-memory-basic-store"
  description = "store for memory acceptance tests"
}

resource "anthropic_memory" "test" {
  memory_store_id = anthropic_memory_store.test.id
  path             = "/notes.md"
  content          = "hello from terraform"
}
`

func TestAccMemoryResource_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             resource.ComposeAggregateTestCheckFunc(testAccCheckMemoryDestroyed, testAccCheckMemoryStoreDestroyed),
		Steps: []resource.TestStep{
			{
				Config: testAccMemoryResourceBasicConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_memory.test", "id"),
					resource.TestCheckResourceAttr("anthropic_memory.test", "path", "/notes.md"),
					resource.TestCheckResourceAttr("anthropic_memory.test", "content", "hello from terraform"),
					resource.TestCheckResourceAttrSet("anthropic_memory.test", "content_sha256"),
					resource.TestCheckResourceAttr("anthropic_memory.test", "content_size_bytes", fmt.Sprintf("%d", len("hello from terraform"))),
					resource.TestCheckResourceAttrSet("anthropic_memory.test", "memory_version_id"),
					resource.TestCheckResourceAttr("anthropic_memory.test", "type", "memory"),
					resource.TestCheckResourceAttrSet("anthropic_memory.test", "created_at"),
					resource.TestCheckResourceAttrSet("anthropic_memory.test", "updated_at"),
				),
			},
		},
	})
}

const testAccMemoryResourceUpdateConfigV1 = `
resource "anthropic_memory_store" "test" {
  name        = "tf-acc-test-memory-update-store"
  description = "store for memory update acceptance test"
}

resource "anthropic_memory" "test" {
  memory_store_id = anthropic_memory_store.test.id
  path             = "/notes.md"
  content          = "version one"
}
`

const testAccMemoryResourceUpdateConfigV2 = `
resource "anthropic_memory_store" "test" {
  name        = "tf-acc-test-memory-update-store"
  description = "store for memory update acceptance test"
}

resource "anthropic_memory" "test" {
  memory_store_id = anthropic_memory_store.test.id
  path             = "/renamed.md"
  content          = "version two, longer content than before"
}
`

func TestAccMemoryResource_update(t *testing.T) {
	var v1Sha, v1VersionID, memoryID string

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             resource.ComposeAggregateTestCheckFunc(testAccCheckMemoryDestroyed, testAccCheckMemoryStoreDestroyed),
		Steps: []resource.TestStep{
			{
				Config: testAccMemoryResourceUpdateConfigV1,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("anthropic_memory.test", "content", "version one"),
					func(s *terraform.State) error {
						rs, ok := s.RootModule().Resources["anthropic_memory.test"]
						if !ok {
							return fmt.Errorf("resource not found in state")
						}
						memoryID = rs.Primary.ID
						v1Sha = rs.Primary.Attributes["content_sha256"]
						v1VersionID = rs.Primary.Attributes["memory_version_id"]
						return nil
					},
				),
			},
			{
				Config: testAccMemoryResourceUpdateConfigV2,
				Check: resource.ComposeAggregateTestCheckFunc(
					// The memory's id is preserved across a path rename.
					func(s *terraform.State) error {
						rs, ok := s.RootModule().Resources["anthropic_memory.test"]
						if !ok {
							return fmt.Errorf("resource not found in state")
						}
						if rs.Primary.ID != memoryID {
							return fmt.Errorf("expected id to be preserved across rename, got %s want %s", rs.Primary.ID, memoryID)
						}
						return nil
					},
					resource.TestCheckResourceAttr("anthropic_memory.test", "path", "/renamed.md"),
					resource.TestCheckResourceAttr("anthropic_memory.test", "content", "version two, longer content than before"),
					resource.TestCheckResourceAttrWith("anthropic_memory.test", "content_sha256", func(v string) error {
						if v == v1Sha {
							return fmt.Errorf("expected content_sha256 to change after content update, still %s", v)
						}
						return nil
					}),
					resource.TestCheckResourceAttrWith("anthropic_memory.test", "memory_version_id", func(v string) error {
						if v == v1VersionID {
							return fmt.Errorf("expected memory_version_id to change after update, still %s", v)
						}
						return nil
					}),
				),
			},
		},
	})
}

func TestAccMemoryResource_import(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             resource.ComposeAggregateTestCheckFunc(testAccCheckMemoryDestroyed, testAccCheckMemoryStoreDestroyed),
		Steps: []resource.TestStep{
			{
				Config: testAccMemoryResourceBasicConfig,
			},
			{
				ResourceName:      "anthropic_memory.test",
				ImportState:       true,
				ImportStateVerify: true,
				ImportStateIdFunc: func(s *terraform.State) (string, error) {
					rs, ok := s.RootModule().Resources["anthropic_memory.test"]
					if !ok {
						return "", fmt.Errorf("resource not found in state")
					}
					return rs.Primary.Attributes["memory_store_id"] + ":" + rs.Primary.ID, nil
				},
			},
		},
	})
}
