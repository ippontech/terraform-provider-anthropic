// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package memorystores_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

// setUpMemoriesFixture creates a memory store with a top-level memory and a
// memory nested one level deeper, to exercise both depth=1 -> prefixes
// roll-up and the path_prefix filter.
func setUpMemoriesFixture(t *testing.T) (storeID string) {
	t.Helper()
	client := newAccTestClient()
	ctx := context.Background()

	store, err := client.Beta.MemoryStores.New(ctx, anthropic.BetaMemoryStoreNewParams{
		Name: "tf-acc-test-memstore-for-memories-ds",
	})
	if err != nil {
		t.Fatalf("unable to create memory store fixture: %s", err)
	}
	t.Cleanup(func() {
		if _, err := client.Beta.MemoryStores.Delete(context.Background(), store.ID, anthropic.BetaMemoryStoreDeleteParams{}); err != nil && !isNotFoundError(err) {
			t.Errorf("cleanup: unable to delete memory store fixture %s: %s", store.ID, err)
		}
	})

	if _, err := client.Beta.MemoryStores.Memories.New(ctx, store.ID, anthropic.BetaMemoryStoreMemoryNewParams{
		Path:    "/top.md",
		Content: param.NewOpt("top-level memory"),
	}); err != nil {
		t.Fatalf("unable to create top-level memory fixture: %s", err)
	}
	if _, err := client.Beta.MemoryStores.Memories.New(ctx, store.ID, anthropic.BetaMemoryStoreMemoryNewParams{
		Path:    "/nested/deep.md",
		Content: param.NewOpt("nested memory"),
	}); err != nil {
		t.Fatalf("unable to create nested memory fixture: %s", err)
	}

	return store.ID
}

func TestAccMemoriesDataSource_basic(t *testing.T) {
	storeID := setUpMemoriesFixture(t)

	recursiveConfig := fmt.Sprintf(`
data "anthropic_memories" "test" {
  memory_store_id = %q
}
`, storeID)

	depthOneConfig := fmt.Sprintf(`
data "anthropic_memories" "test" {
  memory_store_id = %q
  depth            = 1
}
`, storeID)

	pathPrefixConfig := fmt.Sprintf(`
data "anthropic_memories" "test" {
  memory_store_id = %q
  path_prefix      = "/nested/"
  include_content  = true
}
`, storeID)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				// Recursive (default depth): both memories are returned, no
				// prefixes.
				Config: recursiveConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.anthropic_memories.test", "memories.#", "2"),
					resource.TestCheckResourceAttr("data.anthropic_memories.test", "prefixes.#", "0"),
				),
			},
			{
				// depth = 1: the top-level memory is returned directly, the
				// nested one rolls up into a "/nested/" prefix.
				Config: depthOneConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.anthropic_memories.test", "memories.#", "1"),
					resource.TestCheckResourceAttr("data.anthropic_memories.test", "memories.0.path", "/top.md"),
					resource.TestCheckResourceAttr("data.anthropic_memories.test", "prefixes.#", "1"),
					resource.TestCheckResourceAttr("data.anthropic_memories.test", "prefixes.0", "/nested/"),
				),
			},
			{
				// path_prefix filters to the nested memory only;
				// include_content populates its content.
				Config: pathPrefixConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.anthropic_memories.test", "memories.#", "1"),
					resource.TestCheckResourceAttr("data.anthropic_memories.test", "memories.0.path", "/nested/deep.md"),
					resource.TestCheckResourceAttr("data.anthropic_memories.test", "memories.0.content", "nested memory"),
				),
			},
		},
	})
}
