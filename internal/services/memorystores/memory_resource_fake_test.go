// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package memorystores_test

import (
	"fmt"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

const memoryAddr = "anthropic_memory.test"

// memoryConfig is a memory in the store storeID, which the fake already
// holds.
func memoryConfig(store, path, content string) string {
	return providertest.ProviderConfig(sdktest.APIKey) + fmt.Sprintf(`
resource "anthropic_memory" "test" {
  memory_store_id = %q
  path            = %q
  content         = %q
}
`, store, path, content)
}

func expectMemory(id, store, path, content, version, createdAt, updatedAt string) []statecheck.StateCheck {
	check := func(attr string, c knownvalue.Check) statecheck.StateCheck {
		return statecheck.ExpectKnownValue(memoryAddr, tfjsonpath.New(attr), c)
	}
	return []statecheck.StateCheck{
		check("id", knownvalue.StringExact(id)),
		check("memory_store_id", knownvalue.StringExact(store)),
		check("path", knownvalue.StringExact(path)),
		check("content", knownvalue.StringExact(content)),
		check("content_sha256", knownvalue.StringExact(sha(content))),
		check("content_size_bytes", knownvalue.Int64Exact(int64(len(content)))),
		check("memory_version_id", knownvalue.StringExact(version)),
		check("type", knownvalue.StringExact("memory")),
		check("created_at", knownvalue.StringExact(createdAt)),
		check("updated_at", knownvalue.StringExact(updatedAt)),
	}
}

// checkNoMemory is a CheckDestroy: no memory may be left in the fake.
func (f *memoryFake) checkNoMemory(*terraform.State) error {
	if n := f.memories.Len(); n != 0 {
		return fmt.Errorf("%d memories left after destroy", n)
	}
	return nil
}

// TestMemoryResource_lifecycle creates a memory, imports it, changes its
// content and renames it in place, each update guarded by the
// content_sha256 of the state, moves it to another store (replace) and
// deletes it.
func TestMemoryResource_lifecycle(t *testing.T) {
	t.Parallel()
	srv := newMemoryFake(t, 0)
	first := srv.newStore("first", "", nil)["id"].(string)
	second := srv.newStore("second", "", nil)["id"].(string)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkNoMemory,
		Steps: []resource.TestStep{
			{
				Config:            memoryConfig(first, "/notes/todo.md", "buy milk"),
				ConfigPlanChecks:  providertest.ExpectAction(memoryAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectMemory(memoryID(1), first, "/notes/todo.md", "buy milk", versionID(1), at(1), at(1)),
			},
			{
				Config:            memoryConfig(first, "/notes/todo.md", "buy milk"),
				ResourceName:      memoryAddr,
				ImportState:       true,
				ImportStateId:     first + ":" + memoryID(1),
				ImportStateVerify: true,
			},
			{
				Config:            memoryConfig(first, "/notes/todo.md", "buy oat milk"),
				ConfigPlanChecks:  providertest.ExpectAction(memoryAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectMemory(memoryID(1), first, "/notes/todo.md", "buy oat milk", versionID(2), at(1), at(2)),
			},
			{
				Config:            memoryConfig(first, "/notes/done.md", "buy oat milk"),
				ConfigPlanChecks:  providertest.ExpectAction(memoryAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectMemory(memoryID(1), first, "/notes/done.md", "buy oat milk", versionID(3), at(1), at(3)),
			},
			{
				Config:            memoryConfig(second, "/notes/done.md", "buy oat milk"),
				ConfigPlanChecks:  providertest.ExpectAction(memoryAddr, plancheck.ResourceActionReplace),
				ConfigStateChecks: expectMemory(memoryID(2), second, "/notes/done.md", "buy oat milk", versionID(5), at(4), at(4)),
			},
		},
	})

	srv.AssertJSONBodies(routeCreateMemory,
		`{"path": "/notes/todo.md", "content": "buy milk"}`,
		`{"path": "/notes/done.md", "content": "buy oat milk"}`)
	// Only what changed, always behind the precondition of the state.
	srv.AssertJSONBodies(routeUpdateMemory,
		`{"content": "buy oat milk", "precondition": {"type": "content_sha256", "content_sha256": "`+sha("buy milk")+`"}}`,
		`{"path": "/notes/done.md", "precondition": {"type": "content_sha256", "content_sha256": "`+sha("buy oat milk")+`"}}`)
	srv.AssertQueries(routeCreateMemory, "beta=true&view=full", "beta=true&view=full")
	srv.AssertQueries(routeUpdateMemory, "beta=true&view=full", "beta=true&view=full")
	var deleted []string
	for _, r := range srv.Requests(routeDeleteMemory) {
		deleted = append(deleted, r.Path)
	}
	want := []string{
		"/v1/memory_stores/" + first + "/memories/" + memoryID(1),
		"/v1/memory_stores/" + second + "/memories/" + memoryID(2),
	}
	if !slices.Equal(deleted, want) {
		t.Errorf("deletions %q, want %q", deleted, want)
	}
	for _, r := range srv.Requests(routeGetMemory) {
		if v := r.Query.Get("view"); v != "full" && v != "" {
			t.Errorf("GET %s with view=%q", r.Path, v)
		}
	}
	srv.assertRequestShape(t)
}

// TestMemoryResource_removedOutsideTerraform: a memory deleted behind
// Terraform's back is created again; a 404 on the delete is success.
func TestMemoryResource_removedOutsideTerraform(t *testing.T) {
	t.Parallel()
	srv := newMemoryFake(t, 0)
	store := srv.newStore("store", "", nil)["id"].(string)
	config := memoryConfig(store, "/gone.md", "x")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{Config: config, ConfigPlanChecks: providertest.ExpectAction(memoryAddr, plancheck.ResourceActionCreate)},
			{
				PreConfig:         func() { srv.memories.Delete(memoryID(1)) },
				Config:            config,
				ConfigPlanChecks:  providertest.ExpectAction(memoryAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectMemory(memoryID(2), store, "/gone.md", "x", versionID(2), at(2), at(2)),
			},
			{
				PreConfig:        func() { srv.FailNext(routeDeleteMemory, http.StatusNotFound, 1) },
				Config:           providertest.ProviderConfig(sdktest.APIKey),
				ConfigPlanChecks: providertest.ExpectAction(memoryAddr, plancheck.ResourceActionDestroy),
			},
		},
	})

	srv.AssertCalled(routeCreateMemory, 2)
	srv.AssertCalled(routeDeleteMemory, 1)
	srv.assertRequestShape(t)
}

// TestMemoryResource_modifiedOutOfBand: content changed behind Terraform's
// back between the refresh and the update fails the precondition, which
// reaches the operator as such instead of overwriting the remote content.
func TestMemoryResource_modifiedOutOfBand(t *testing.T) {
	t.Parallel()
	srv := newMemoryFake(t, 0)
	store := srv.newStore("store", "", nil)["id"].(string)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkNoMemory,
		Steps: []resource.TestStep{
			{Config: memoryConfig(store, "/doc.md", "v1"), ConfigPlanChecks: providertest.ExpectAction(memoryAddr, plancheck.ResourceActionCreate)},
			{
				// The precondition the update sends no longer matches: the
				// refresh read "v1", then another writer changed it.
				PreConfig:   func() { srv.raceNextUpdate("v1 edited elsewhere") },
				Config:      memoryConfig(store, "/doc.md", "v2"),
				ExpectError: regexp.MustCompile(`(?s)Memory\s+Modified\s+Out-Of-Band.*content_sha256\s+precondition\s+failed`),
			},
		},
	})

	srv.AssertCalled(routeUpdateMemory, 1)
	// The history: created, the other writer's edit, the final delete; the
	// rejected v2 never landed.
	var history []string
	for _, v := range srv.versions.List(nil) {
		history = append(history, fmt.Sprintf("%v %v", v["operation"], v["content"]))
	}
	if want := []string{"created v1", "modified v1 edited elsewhere", "deleted <nil>"}; !slices.Equal(history, want) {
		t.Errorf("versions %q, want %q", history, want)
	}
}

// TestMemoryResource_apiErrors: a path already taken fails the create and a
// rename (409 conflict_error, not the precondition), and an API error on
// refresh or delete reaches the operator with the provider's summary.
func TestMemoryResource_apiErrors(t *testing.T) {
	t.Parallel()
	srv := newMemoryFake(t, 0)
	store := srv.newStore("store", "", nil)["id"].(string)
	srv.newMemory(store, "/taken.md", "other")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{
				PreConfig: func() {
					srv.FailNextWith(routeCreateMemory, http.StatusConflict, "a memory already exists at /taken.md", 1)
				},
				Config:      memoryConfig(store, "/taken.md", "mine"),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+create\s+memory:.*409.*already\s+exists`),
			},
			{Config: memoryConfig(store, "/mine.md", "mine"), ConfigPlanChecks: providertest.ExpectAction(memoryAddr, plancheck.ResourceActionCreate)},
			{
				PreConfig: func() {
					srv.FailNextWith(routeUpdateMemory, http.StatusConflict, "a memory already exists at /taken.md", 1)
				},
				Config:      memoryConfig(store, "/taken.md", "mine"),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+update\s+memory:.*409.*already\s+exists`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeGetMemory, http.StatusForbidden, 1) },
				Config:      memoryConfig(store, "/mine.md", "mine"),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+read\s+memory:.*403`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeDeleteMemory, http.StatusBadRequest, 1) },
				Config:      providertest.ProviderConfig(sdktest.APIKey),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+delete\s+memory:.*400`),
			},
		},
	})

	srv.AssertCalled(routeCreateMemory, 2)
	srv.AssertCalled(routeUpdateMemory, 1)
	// The failed delete, then the one of the final destroy.
	srv.AssertCalled(routeDeleteMemory, 2)
	srv.assertRequestShape(t)
}

// TestMemoryResource_importInvalidID: an import ID that is not
// <memory_store_id>:<memory_id> is refused before any request.
func TestMemoryResource_importInvalidID(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"mem_only", ":mem_1", "memstore_1:"} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			srv := newMemoryFake(t, 0)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
				Steps: []resource.TestStep{{
					Config:        memoryConfig("memstore_1", "/a.md", "a"),
					ResourceName:  memoryAddr,
					ImportState:   true,
					ImportStateId: id,
					ExpectError:   regexp.MustCompile(`Invalid\s+import\s+ID`),
				}},
			})
			srv.AssertCalled("", 0)
		})
	}
}

// TestMemoryResource_invalidConfig: the path and content validators refuse
// what the API would, at plan time and before any request.
func TestMemoryResource_invalidConfig(t *testing.T) {
	t.Parallel()
	srv := newMemoryFake(t, 0)
	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{
				Config:      memoryConfig("memstore_1", "notes/a.md", "a"),
				ExpectError: regexp.MustCompile(`(?s)Invalid\s+path.*must\s+start\s+with\s+'/'`),
			},
			{
				Config:      memoryConfig("memstore_1", "/a.md", strings.Repeat("a", 102401)),
				ExpectError: regexp.MustCompile(`(?s)Invalid\s+content.*at\s+most\s+102400\s+bytes`),
			},
		},
	})
	srv.AssertCalled("", 0)
}
