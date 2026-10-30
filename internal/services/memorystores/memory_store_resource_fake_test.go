// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package memorystores_test

import (
	"fmt"
	"net/http"
	"regexp"
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

const storeAddr = "anthropic_memory_store.test"

// storeID, memoryID and versionID are the IDs the fake gives to the n-th
// object of each collection.
func storeID(n int) string   { return fmt.Sprintf("memstore_%024d", n) }
func memoryID(n int) string  { return fmt.Sprintf("mem_%024d", n) }
func versionID(n int) string { return fmt.Sprintf("memver_%024d", n) }

// at is the n-th tick of a fake collection's clock.
func at(n int) string { return fmt.Sprintf("2026-01-01T00:00:%02dZ", n) }

func storeConfig(extra string) string {
	return providertest.ProviderConfig(sdktest.APIKey) + `
resource "anthropic_memory_store" "test" {
` + extra + `}
`
}

func expectAction(addr string, action plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{
		PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, action)},
		PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
	}
}

// stringMap is the exact check of a map of strings, or of null when m is
// nil.
func stringMap(m map[string]string) knownvalue.Check {
	if m == nil {
		return knownvalue.Null()
	}
	checks := map[string]knownvalue.Check{}
	for k, v := range m {
		checks[k] = knownvalue.StringExact(v)
	}
	return knownvalue.MapExact(checks)
}

func stringOrNull(s string) knownvalue.Check {
	if s == "" {
		return knownvalue.Null()
	}
	return knownvalue.StringExact(s)
}

func expectStore(id, name, description string, metadata map[string]string, createdAt, archivedAt string, archiveOnDestroy bool) []statecheck.StateCheck {
	check := func(attr string, c knownvalue.Check) statecheck.StateCheck {
		return statecheck.ExpectKnownValue(storeAddr, tfjsonpath.New(attr), c)
	}
	return []statecheck.StateCheck{
		check("id", knownvalue.StringExact(id)),
		check("name", knownvalue.StringExact(name)),
		check("description", knownvalue.StringExact(description)),
		check("metadata", stringMap(metadata)),
		check("created_at", knownvalue.StringExact(createdAt)),
		check("archived_at", stringOrNull(archivedAt)),
		check("archive_on_destroy", knownvalue.Bool(archiveOnDestroy)),
	}
}

// TestMemoryStoreResource_lifecycle creates a store with metadata, imports
// it, updates it in place with a metadata patch that removes a key, clears
// the metadata to {}, flips archive_on_destroy without an API call, and
// archives it on destroy.
func TestMemoryStoreResource_lifecycle(t *testing.T) {
	t.Parallel()
	srv := newMemoryFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy: func(*terraform.State) error {
			if s, ok := srv.stores.Get(storeID(1)); !ok || s["archived_at"] == nil {
				return fmt.Errorf("store %s is not archived after destroy: %v", storeID(1), s)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config:            storeConfig("  name = \"notes\"\n  description = \"Team notes\"\n  metadata = { team = \"core\", env = \"dev\" }\n"),
				ConfigPlanChecks:  expectAction(storeAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectStore(storeID(1), "notes", "Team notes", map[string]string{"team": "core", "env": "dev"}, at(1), "", false),
			},
			{
				Config:            storeConfig("  name = \"notes\"\n  description = \"Team notes\"\n  metadata = { team = \"core\", env = \"dev\" }\n"),
				ResourceName:      storeAddr,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config:            storeConfig("  name = \"notes-v2\"\n  description = \"Team notes v2\"\n  metadata = { team = \"platform\" }\n"),
				ConfigPlanChecks:  expectAction(storeAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectStore(storeID(1), "notes-v2", "Team notes v2", map[string]string{"team": "platform"}, at(1), "", false),
			},
			{
				Config:            storeConfig("  name = \"notes-v2\"\n  description = \"Team notes v2\"\n  metadata = {}\n"),
				ConfigPlanChecks:  expectAction(storeAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectStore(storeID(1), "notes-v2", "Team notes v2", map[string]string{}, at(1), "", false),
			},
			{
				Config:            storeConfig("  name = \"notes-v2\"\n  description = \"Team notes v2\"\n  metadata = {}\n  archive_on_destroy = true\n"),
				ConfigPlanChecks:  expectAction(storeAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectStore(storeID(1), "notes-v2", "Team notes v2", map[string]string{}, at(1), "", true),
			},
		},
	})

	srv.AssertJSONBodies(routeCreateStore, `{"name": "notes", "description": "Team notes", "metadata": {"team": "core", "env": "dev"}}`)
	// The archive_on_destroy step sends nothing.
	srv.AssertJSONBodies(routeUpdateStore,
		`{"name": "notes-v2", "description": "Team notes v2", "metadata": {"team": "platform", "env": null}}`,
		`{"name": "notes-v2", "description": "Team notes v2", "metadata": {"team": null}}`)
	srv.AssertCalled(routeArchiveStore, 1)
	srv.AssertCalled(routeDeleteStore, 0)
	srv.assertRequestShape(t)
}

// TestMemoryStoreResource_deleteAndRecreate: without archive_on_destroy the
// destroy deletes the store; a store deleted behind Terraform's back is
// created again, and a 404 on the delete is success.
func TestMemoryStoreResource_deleteAndRecreate(t *testing.T) {
	t.Parallel()
	srv := newMemoryFake(t, 0)
	config := storeConfig("  name = \"scratch\"\n  description = \"\"\n")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy: func(*terraform.State) error {
			if n := srv.stores.Len(); n != 1 {
				return fmt.Errorf("%d stores left, want the one whose delete met the injected 404", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config:            config,
				ConfigPlanChecks:  expectAction(storeAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectStore(storeID(1), "scratch", "", nil, at(1), "", false),
			},
			{
				PreConfig:         func() { srv.stores.Delete(storeID(1)) },
				Config:            config,
				ConfigPlanChecks:  expectAction(storeAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectStore(storeID(2), "scratch", "", nil, at(2), "", false),
			},
			{
				PreConfig:        func() { srv.FailNext(routeDeleteStore, http.StatusNotFound, 1) },
				Config:           providertest.ProviderConfig(sdktest.APIKey),
				ConfigPlanChecks: expectAction(storeAddr, plancheck.ResourceActionDestroy),
			},
		},
	})

	srv.AssertJSONBodies(routeCreateStore, `{"name": "scratch", "description": ""}`, `{"name": "scratch", "description": ""}`)
	srv.AssertCalled(routeDeleteStore, 1)
	srv.AssertCalled(routeArchiveStore, 0)
	srv.assertRequestShape(t)
}

// TestMemoryStoreResource_archivedOutsideTerraform: a store archived behind
// Terraform's back shows its archived_at on refresh, refuses a change of
// name before any request, and is not archived again on destroy.
func TestMemoryStoreResource_archivedOutsideTerraform(t *testing.T) {
	t.Parallel()
	srv := newMemoryFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{
				Config:           storeConfig("  name = \"kept\"\n  description = \"Kept\"\n  archive_on_destroy = true\n"),
				ConfigPlanChecks: expectAction(storeAddr, plancheck.ResourceActionCreate),
			},
			{
				PreConfig:         func() { srv.stores.Archive(storeID(1)) },
				Config:            storeConfig("  name = \"kept\"\n  description = \"Kept\"\n  archive_on_destroy = true\n"),
				ConfigPlanChecks:  expectAction(storeAddr, plancheck.ResourceActionNoop),
				ConfigStateChecks: expectStore(storeID(1), "kept", "Kept", nil, at(1), at(2), true),
			},
			{
				Config:      storeConfig("  name = \"renamed\"\n  description = \"Kept\"\n  archive_on_destroy = true\n"),
				ExpectError: regexp.MustCompile(`(?s)Memory\s+Store\s+Is\s+Archived.*` + storeID(1)),
			},
		},
	})

	srv.AssertCalled(routeUpdateStore, 0)
	srv.AssertCalled(routeArchiveStore, 0)
	srv.AssertCalled(routeDeleteStore, 0)
}

// TestMemoryStoreResource_apiErrors: an API error on create, refresh,
// update, archive or delete reaches the operator with the provider's
// summary.
func TestMemoryStoreResource_apiErrors(t *testing.T) {
	t.Parallel()
	srv := newMemoryFake(t, 0)
	config := func(name string, archive bool) string {
		return storeConfig(fmt.Sprintf("  name = %q\n  description = \"d\"\n  archive_on_destroy = %t\n", name, archive))
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy: func(*terraform.State) error {
			if n := srv.stores.Len(); n != 0 {
				return fmt.Errorf("%d stores left after destroy", n)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				PreConfig:   func() { srv.FailNext(routeCreateStore, http.StatusBadRequest, 1) },
				Config:      config("errors", false),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+create\s+memory\s+store:.*400`),
			},
			{Config: config("errors", false), ConfigPlanChecks: expectAction(storeAddr, plancheck.ResourceActionCreate)},
			{
				PreConfig:   func() { srv.FailNext(routeGetStore, http.StatusForbidden, 1) },
				Config:      config("errors", false),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+read\s+memory\s+store:.*403`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeUpdateStore, http.StatusBadRequest, 1) },
				Config:      config("renamed", false),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+update\s+memory\s+store:.*400`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeDeleteStore, http.StatusBadRequest, 1) },
				Config:      providertest.ProviderConfig(sdktest.APIKey),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+delete\s+memory\s+store:.*400`),
			},
			{Config: config("errors", true), ConfigPlanChecks: expectAction(storeAddr, plancheck.ResourceActionUpdate)},
			{
				PreConfig:   func() { srv.FailNext(routeArchiveStore, http.StatusBadRequest, 1) },
				Config:      providertest.ProviderConfig(sdktest.APIKey),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+archive\s+memory\s+store:.*400`),
			},
			{
				PreConfig:        func() { srv.FailNext(routeArchiveStore, http.StatusNotFound, 1) },
				Config:           providertest.ProviderConfig(sdktest.APIKey),
				ConfigPlanChecks: expectAction(storeAddr, plancheck.ResourceActionDestroy),
			},
			{
				// Gone for good: archive_on_destroy kept the 404'd store, so
				// clean it up as an operator would.
				PreConfig: func() { srv.stores.Delete(storeID(1)) },
				Config:    providertest.ProviderConfig(sdktest.APIKey),
			},
		},
	})

	srv.AssertCalled(routeCreateStore, 2)
	srv.AssertCalled(routeUpdateStore, 1)
	srv.AssertCalled(routeDeleteStore, 1)
	srv.AssertCalled(routeArchiveStore, 2)
	srv.assertRequestShape(t)
}
