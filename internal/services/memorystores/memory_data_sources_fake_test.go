// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package memorystores_test

import (
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// dataStep is a single plan-and-apply step reading block, with exact checks
// on attrs of addr.
func dataStep(block, addr string, attrs map[string]knownvalue.Check) resource.TestStep {
	var checks []statecheck.StateCheck
	for name, c := range attrs {
		checks = append(checks, statecheck.ExpectKnownValue(addr, tfjsonpath.New(name), c))
	}
	return resource.TestStep{Config: providertest.ProviderConfig(sdktest.APIKey) + block, ConfigStateChecks: checks}
}

// errorStep is a step reading block that must fail with pattern.
func errorStep(block, pattern string) resource.TestStep {
	return resource.TestStep{Config: providertest.ProviderConfig(sdktest.APIKey) + block, ExpectError: regexp.MustCompile(pattern)}
}

func unitTest(t *testing.T, srv *memoryFake, steps ...resource.TestStep) {
	t.Helper()
	resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server), Steps: steps})
}

func storeObject(id, name, description string, metadata map[string]string, createdAt, archivedAt string) knownvalue.Check {
	return knownvalue.ObjectExact(map[string]knownvalue.Check{
		"id":          knownvalue.StringExact(id),
		"name":        knownvalue.StringExact(name),
		"description": knownvalue.StringExact(description),
		"metadata":    stringMap(metadata),
		"created_at":  knownvalue.StringExact(createdAt),
		"archived_at": stringOrNull(archivedAt),
	})
}

// fourStores stores four stores and archives the second.
func (f *memoryFake) fourStores() {
	f.newStore("first", "One", map[string]any{"team": "core"})
	f.newStore("second", "", nil)
	f.newStore("third", "", nil)
	f.newStore("fourth", "", nil)
	f.stores.Archive(storeID(2))
}

// TestMemoryStoreDataSource_read reads a store, metadata included, and an
// archived one; an unknown ID is an error.
func TestMemoryStoreDataSource_read(t *testing.T) {
	t.Parallel()
	srv := newMemoryFake(t, 0)
	srv.fourStores()
	const addr = "data.anthropic_memory_store.test"

	unitTest(t, srv,
		dataStep(`data "anthropic_memory_store" "test" { id = "`+storeID(1)+`" }`, addr, map[string]knownvalue.Check{
			"id": knownvalue.StringExact(storeID(1)), "name": knownvalue.StringExact("first"), "description": knownvalue.StringExact("One"),
			"metadata": stringMap(map[string]string{"team": "core"}), "created_at": knownvalue.StringExact(at(1)), "archived_at": knownvalue.Null(),
		}),
		dataStep(`data "anthropic_memory_store" "test" { id = "`+storeID(2)+`" }`, addr, map[string]knownvalue.Check{
			"description": knownvalue.StringExact(""), "metadata": knownvalue.Null(), "archived_at": knownvalue.StringExact(at(5)),
		}),
		errorStep(`data "anthropic_memory_store" "test" { id = "memstore_missing" }`, `(?s)Unable\s+to\s+retrieve\s+memory\s+store:.*404`),
	)
	srv.assertRequestShape(t)
}

// TestMemoryStoresDataSource_pages lists the live stores over two pages,
// newest first.
func TestMemoryStoresDataSource_pages(t *testing.T) {
	t.Parallel()
	srv := newMemoryFake(t, 2)
	srv.fourStores()

	unitTest(t, srv, dataStep(`data "anthropic_memory_stores" "test" {}`, "data.anthropic_memory_stores.test", map[string]knownvalue.Check{
		"memory_stores": knownvalue.ListExact([]knownvalue.Check{
			storeObject(storeID(4), "fourth", "", nil, at(4), ""),
			storeObject(storeID(3), "third", "", nil, at(3), ""),
			storeObject(storeID(1), "first", "One", map[string]string{"team": "core"}, at(1), ""),
		}),
	}))
	srv.AssertWalks(routeListStores, "beta=true", "beta=true&page="+sdktest.NextPageToken(storeID(3)))
	srv.AssertAllPagesRead(routeListStores)
	srv.assertRequestShape(t)
}

// TestMemoryStoresDataSource_includeArchived sends include_archived=true and
// lists the archived store too.
func TestMemoryStoresDataSource_includeArchived(t *testing.T) {
	t.Parallel()
	srv := newMemoryFake(t, 2)
	srv.fourStores()

	unitTest(t, srv, dataStep(`data "anthropic_memory_stores" "test" { include_archived = true }`, "data.anthropic_memory_stores.test", map[string]knownvalue.Check{
		"memory_stores": knownvalue.ListExact([]knownvalue.Check{
			storeObject(storeID(4), "fourth", "", nil, at(4), ""),
			storeObject(storeID(3), "third", "", nil, at(3), ""),
			storeObject(storeID(2), "second", "", nil, at(2), at(5)),
			storeObject(storeID(1), "first", "One", map[string]string{"team": "core"}, at(1), ""),
		}),
	}))
	srv.AssertWalks(routeListStores, "beta=true&include_archived=true", "beta=true&include_archived=true&page="+sdktest.NextPageToken(storeID(3)))
	srv.AssertAllPagesRead(routeListStores)
}

// TestMemoryStoresDataSource_emptyAndError: no store is an empty list,
// never null, and include_archived = false sends nothing; a failed list is
// an error.
func TestMemoryStoresDataSource_emptyAndError(t *testing.T) {
	t.Parallel()
	srv := newMemoryFake(t, 0)

	unitTest(t, srv,
		dataStep(`data "anthropic_memory_stores" "test" { include_archived = false }`, "data.anthropic_memory_stores.test", map[string]knownvalue.Check{
			"memory_stores": knownvalue.ListExact([]knownvalue.Check{}),
		}),
		resource.TestStep{
			PreConfig:   func() { srv.FailNext(routeListStores, http.StatusForbidden, 1) },
			Config:      providertest.ProviderConfig(sdktest.APIKey) + `data "anthropic_memory_stores" "other" {}`,
			ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+list\s+memory\s+stores:.*403`),
		},
	)
	for _, r := range srv.Requests(routeListStores) {
		if r.Query.Has("include_archived") {
			t.Errorf("GET %s?%s sends include_archived", r.Path, r.Query.Encode())
		}
	}
}

// TestMemoryDataSource_read reads a memory with its content (view=full); an
// unknown memory is an error.
func TestMemoryDataSource_read(t *testing.T) {
	t.Parallel()
	srv := newMemoryFake(t, 0)
	store := srv.newStore("store", "", nil)["id"].(string)
	srv.newMemory(store, "/notes/a.md", "alpha")
	const addr = "data.anthropic_memory.test"

	unitTest(t, srv,
		dataStep(`data "anthropic_memory" "test" {
  memory_store_id = "`+store+`"
  id              = "`+memoryID(1)+`"
}`, addr, map[string]knownvalue.Check{
			"memory_store_id": knownvalue.StringExact(store), "id": knownvalue.StringExact(memoryID(1)),
			"path": knownvalue.StringExact("/notes/a.md"), "content": knownvalue.StringExact("alpha"),
			"content_sha256": knownvalue.StringExact(sha("alpha")), "content_size_bytes": knownvalue.Int64Exact(5),
			"memory_version_id": knownvalue.StringExact(versionID(1)), "type": knownvalue.StringExact("memory"),
			"created_at": knownvalue.StringExact(at(1)), "updated_at": knownvalue.StringExact(at(1)),
		}),
		errorStep(`data "anthropic_memory" "test" {
  memory_store_id = "`+store+`"
  id              = "mem_missing"
}`, `(?s)Unable\s+to\s+retrieve\s+memory:.*404`),
	)
	srv.AssertWalks(routeGetMemory, "beta=true&view=full")
	srv.assertRequestShape(t)
}

func memoryObject(id, path string, content any, version, createdAt string) knownvalue.Check {
	c := knownvalue.Check(knownvalue.Null())
	if content != nil {
		c = knownvalue.StringExact(content.(string))
	}
	text := map[string]string{memoryID(1): "alpha", memoryID(2): "beta", memoryID(3): "gamma", memoryID(4): "delta"}[id]
	return knownvalue.ObjectExact(map[string]knownvalue.Check{
		"id": knownvalue.StringExact(id), "path": knownvalue.StringExact(path), "content": c,
		"content_sha256": knownvalue.StringExact(sha(text)), "content_size_bytes": knownvalue.Int64Exact(int64(len(text))),
		"memory_version_id": knownvalue.StringExact(version),
		"created_at":        knownvalue.StringExact(createdAt), "updated_at": knownvalue.StringExact(createdAt),
	})
}

// fourMemories stores four memories in a new store and returns its ID.
func (f *memoryFake) fourMemories() string {
	store := f.newStore("store", "", nil)["id"].(string)
	f.newMemory(store, "/notes/a.md", "alpha")
	f.newMemory(store, "/notes/deep/b.md", "beta")
	f.newMemory(store, "/notes/deep/c.md", "gamma")
	f.newMemory(store, "/other.md", "delta")
	return store
}

// TestMemoriesDataSource_pages lists every memory of the store over two
// pages, without content (view=basic).
func TestMemoriesDataSource_pages(t *testing.T) {
	t.Parallel()
	srv := newMemoryFake(t, 3)
	store := srv.fourMemories()

	unitTest(t, srv, dataStep(`data "anthropic_memories" "test" { memory_store_id = "`+store+`" }`, "data.anthropic_memories.test", map[string]knownvalue.Check{
		"memories": knownvalue.ListExact([]knownvalue.Check{
			memoryObject(memoryID(1), "/notes/a.md", nil, versionID(1), at(1)),
			memoryObject(memoryID(2), "/notes/deep/b.md", nil, versionID(2), at(2)),
			memoryObject(memoryID(3), "/notes/deep/c.md", nil, versionID(3), at(3)),
			memoryObject(memoryID(4), "/other.md", nil, versionID(4), at(4)),
		}),
		"prefixes": knownvalue.ListExact([]knownvalue.Check{}),
	}))
	srv.AssertWalks(routeListMemories, "beta=true&view=basic", "beta=true&page="+sdktest.NextPageToken(memoryID(3))+"&view=basic")
	srv.AssertAllPagesRead(routeListMemories)
	srv.assertRequestShape(t)
}

// TestMemoriesDataSource_depthAndContent: path_prefix and depth = 1 return
// the direct children with their content and roll the deeper ones up into
// prefixes.
func TestMemoriesDataSource_depthAndContent(t *testing.T) {
	t.Parallel()
	srv := newMemoryFake(t, 0)
	store := srv.fourMemories()

	unitTest(t, srv, dataStep(`data "anthropic_memories" "test" {
  memory_store_id = "`+store+`"
  path_prefix     = "/notes/"
  depth           = 1
  include_content = true
}`, "data.anthropic_memories.test", map[string]knownvalue.Check{
		"memories": knownvalue.ListExact([]knownvalue.Check{memoryObject(memoryID(1), "/notes/a.md", "alpha", versionID(1), at(1))}),
		"prefixes": knownvalue.ListExact([]knownvalue.Check{knownvalue.StringExact("/notes/deep/")}),
	}))
	srv.AssertWalks(routeListMemories, "beta=true&depth=1&path_prefix=%2Fnotes%2F&view=full")
}

// TestMemoriesDataSource_emptyAndNotFound: a store without memories is an
// empty list, never null; an unknown store is an error.
func TestMemoriesDataSource_emptyAndNotFound(t *testing.T) {
	t.Parallel()
	srv := newMemoryFake(t, 0)
	store := srv.newStore("empty", "", nil)["id"].(string)

	unitTest(t, srv,
		dataStep(`data "anthropic_memories" "test" { memory_store_id = "`+store+`" }`, "data.anthropic_memories.test", map[string]knownvalue.Check{
			"memories": knownvalue.ListExact([]knownvalue.Check{}),
			"prefixes": knownvalue.ListExact([]knownvalue.Check{}),
		}),
		errorStep(`data "anthropic_memories" "test" { memory_store_id = "memstore_missing" }`, `(?s)Unable\s+to\s+list\s+memories:.*404`),
	)
}
