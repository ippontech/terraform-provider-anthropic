// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package memorystores_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

// setUpMemoryFixture creates a memory store and a memory inside it via the
// SDK directly. anthropic_memory is a read-only lookup with no managed
// resource counterpart on this branch (the sibling anthropic_memory resource
// is implemented on a different branch, #121), so the fixture it reads has to
// be created out of band. Cleanup is registered via t.Cleanup in LIFO order
// (memory deleted before the store that holds it), so the test never relies
// on the store delete cascading to its memories.
func setUpMemoryFixture(t *testing.T, namePrefix, path, content string) (storeID, memoryID string) {
	t.Helper()
	client := newAccTestClient()
	ctx := context.Background()

	store, err := client.Beta.MemoryStores.New(ctx, anthropic.BetaMemoryStoreNewParams{
		Name: namePrefix,
	})
	if err != nil {
		t.Fatalf("unable to create memory store fixture: %s", err)
	}
	t.Cleanup(func() {
		if _, err := client.Beta.MemoryStores.Delete(context.Background(), store.ID, anthropic.BetaMemoryStoreDeleteParams{}); err != nil && !isNotFoundError(err) {
			t.Errorf("cleanup: unable to delete memory store fixture %s: %s", store.ID, err)
		}
	})

	memory, err := client.Beta.MemoryStores.Memories.New(ctx, store.ID, anthropic.BetaMemoryStoreMemoryNewParams{
		Path:    path,
		Content: param.NewOpt(content),
	})
	if err != nil {
		t.Fatalf("unable to create memory fixture: %s", err)
	}
	t.Cleanup(func() {
		if _, err := client.Beta.MemoryStores.Memories.Delete(context.Background(), memory.ID, anthropic.BetaMemoryStoreMemoryDeleteParams{
			MemoryStoreID: store.ID,
		}); err != nil && !isNotFoundError(err) {
			t.Errorf("cleanup: unable to delete memory fixture %s: %s", memory.ID, err)
		}
	})

	return store.ID, memory.ID
}

func TestAccMemoryDataSource_basic(t *testing.T) {
	if os.Getenv(resource.EnvTfAcc) == "" {
		t.Skipf("acceptance test skipped unless %s is set", resource.EnvTfAcc)
	}
	storeID, memoryID := setUpMemoryFixture(t, "tf-acc-test-memstore-for-memory-ds", "/notes/foo.md", "hello from acceptance test")

	config := fmt.Sprintf(`
data "anthropic_memory" "test" {
  memory_store_id = %q
  id               = %q
}
`, storeID, memoryID)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.anthropic_memory.test", "id", memoryID),
					resource.TestCheckResourceAttr("data.anthropic_memory.test", "memory_store_id", storeID),
					resource.TestCheckResourceAttr("data.anthropic_memory.test", "path", "/notes/foo.md"),
					resource.TestCheckResourceAttr("data.anthropic_memory.test", "content", "hello from acceptance test"),
					resource.TestCheckResourceAttr("data.anthropic_memory.test", "type", "memory"),
					resource.TestCheckResourceAttrSet("data.anthropic_memory.test", "content_sha256"),
					resource.TestCheckResourceAttrSet("data.anthropic_memory.test", "content_size_bytes"),
					resource.TestCheckResourceAttrSet("data.anthropic_memory.test", "memory_version_id"),
					resource.TestCheckResourceAttrSet("data.anthropic_memory.test", "created_at"),
					resource.TestCheckResourceAttrSet("data.anthropic_memory.test", "updated_at"),
				),
			},
		},
	})
}
