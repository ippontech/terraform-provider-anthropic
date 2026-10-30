// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package vaults_test

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
)

const vaultAddr = "anthropic_vault.test"

// vaultID and credentialID are the IDs the fake gives to the n-th object of
// each collection.
func vaultID(n int) string      { return fmt.Sprintf("vlt_%024d", n) }
func credentialID(n int) string { return fmt.Sprintf("vcrd_%024d", n) }

// at is the n-th tick of a fake collection's clock.
func at(n int) string { return fmt.Sprintf("2026-01-01T00:00:%02dZ", n) }

func vaultConfig(extra string) string {
	return config(`
resource "anthropic_vault" "test" {
  display_name = "team vault"
` + extra + `}
`)
}

// expect checks attributes of addr in state.
func expect(addr string, values map[string]knownvalue.Check) []statecheck.StateCheck {
	checks := make([]statecheck.StateCheck, 0, len(values))
	for name, v := range values {
		checks = append(checks, statecheck.ExpectKnownValue(addr, tfjsonpath.New(name), v))
	}
	return checks
}

func stringMap(values map[string]string) knownvalue.Check {
	checks := map[string]knownvalue.Check{}
	for k, v := range values {
		checks[k] = knownvalue.StringExact(v)
	}
	return knownvalue.MapExact(checks)
}

// checkVaultsGone is a CheckDestroy: the fake holds no vault.
func (f *vaultsFake) checkVaultsGone(*terraform.State) error {
	if n := f.vaults.Len(); n != 0 {
		return fmt.Errorf("%d vaults left after destroy", n)
	}
	return nil
}

// TestVaultResource_lifecycle creates a vault with metadata, imports it,
// renames it and patches its metadata in place, and deletes it.
func TestVaultResource_lifecycle(t *testing.T) {
	t.Parallel()
	srv := newVaultsFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(srv),
		CheckDestroy:             srv.checkVaultsGone,
		Steps: []resource.TestStep{
			{
				Config:           vaultConfig("  metadata = { team = \"a\", env = \"dev\" }\n"),
				ConfigPlanChecks: providertest.ExpectAction(vaultAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expect(vaultAddr, map[string]knownvalue.Check{
					"id":                 knownvalue.StringExact(vaultID(1)),
					"display_name":       knownvalue.StringExact("team vault"),
					"metadata":           stringMap(map[string]string{"team": "a", "env": "dev"}),
					"type":               knownvalue.StringExact("vault"),
					"created_at":         knownvalue.StringExact(at(1)),
					"updated_at":         knownvalue.StringExact(at(1)),
					"archived_at":        knownvalue.Null(),
					"archive_on_destroy": knownvalue.Bool(false),
				}),
			},
			{
				Config:            vaultConfig("  metadata = { team = \"a\", env = \"dev\" }\n"),
				ResourceName:      vaultAddr,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config: config(`
resource "anthropic_vault" "test" {
  display_name = "renamed vault"
  metadata     = { team = "b", owner = "ops" }
}
`),
				ConfigPlanChecks: providertest.ExpectAction(vaultAddr, plancheck.ResourceActionUpdate),
				// The update, then the metadata patch: two ticks.
				ConfigStateChecks: expect(vaultAddr, map[string]knownvalue.Check{
					"display_name": knownvalue.StringExact("renamed vault"),
					"metadata":     stringMap(map[string]string{"team": "b", "owner": "ops"}),
					"created_at":   knownvalue.StringExact(at(1)),
					"updated_at":   knownvalue.StringExact(at(3)),
				}),
			},
			{
				// Removing metadata deletes every key.
				Config:            vaultConfig(""),
				ConfigPlanChecks:  providertest.ExpectAction(vaultAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expect(vaultAddr, map[string]knownvalue.Check{"metadata": knownvalue.Null()}),
			},
		},
	})

	srv.AssertJSONBodies(routeCreateVault, `{"display_name": "team vault", "metadata": {"team": "a", "env": "dev"}}`)
	srv.AssertJSONBodies(routeUpdateVault,
		`{"display_name": "renamed vault", "metadata": {"team": "b", "owner": "ops", "env": null}}`,
		`{"display_name": "team vault", "metadata": {"team": null, "owner": null}}`)
	srv.AssertCalled(routeDeleteVault, 1)
	srv.AssertCalled(routeArchiveVault, 0)
	srv.assertRequestShape()
}

// TestVaultResource_archiveOnDestroy: archive_on_destroy archives the vault
// and deletes nothing; an import reads false.
func TestVaultResource_archiveOnDestroy(t *testing.T) {
	t.Parallel()
	srv := newVaultsFake(t, 0)
	cfg := vaultConfig("  archive_on_destroy = true\n")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(srv),
		CheckDestroy: func(*terraform.State) error {
			if o, ok := srv.vaults.Get(vaultID(1)); !ok || o["archived_at"] != at(2) {
				return fmt.Errorf("vault after destroy: %v (found %t), want archived at %s", o, ok, at(2))
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config:            cfg,
				ConfigPlanChecks:  providertest.ExpectAction(vaultAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expect(vaultAddr, map[string]knownvalue.Check{"archive_on_destroy": knownvalue.Bool(true), "metadata": knownvalue.Null()}),
			},
			{
				Config:                  cfg,
				ResourceName:            vaultAddr,
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"archive_on_destroy"},
				ImportStateCheck: func(states []*terraform.InstanceState) error {
					if got := states[0].Attributes["archive_on_destroy"]; got != "false" {
						return fmt.Errorf("imported archive_on_destroy = %q, want false", got)
					}
					return nil
				},
			},
		},
	})

	srv.AssertJSONBodies(routeCreateVault, `{"display_name": "team vault"}`)
	srv.AssertCalled(routeArchiveVault, 1)
	srv.AssertCalled(routeDeleteVault, 0)
	srv.assertRequestShape()
}

// TestVaultResource_emptyMetadata: metadata = {} is sent as {} and stays an
// empty map in state, although the API answers {} for no metadata too
// (root CLAUDE.md, "Map attributes with PATCH semantics").
func TestVaultResource_emptyMetadata(t *testing.T) {
	t.Skip("bug: metadata = {} reads back as null (inconsistent result after apply); see fix/vaults-empty-metadata")
	t.Parallel()
	srv := newVaultsFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(srv),
		CheckDestroy:             srv.checkVaultsGone,
		Steps: []resource.TestStep{
			{
				Config:            vaultConfig("  metadata = {}\n"),
				ConfigPlanChecks:  providertest.ExpectAction(vaultAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expect(vaultAddr, map[string]knownvalue.Check{"metadata": knownvalue.MapExact(map[string]knownvalue.Check{})}),
			},
		},
	})
}

// TestVaultResource_goneOutOfBand: a vault deleted behind Terraform's back is
// re-created by the next apply; destroy succeeds when the delete or the
// archive answers 404.
func TestVaultResource_goneOutOfBand(t *testing.T) {
	t.Parallel()
	srv := newVaultsFake(t, 0)
	cfg := config(`
resource "anthropic_vault" "test" {
  display_name = "team vault"
}

resource "anthropic_vault" "archived" {
  display_name       = "archived vault"
  archive_on_destroy = true
}
`)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(srv),
		Steps: []resource.TestStep{
			{Config: cfg},
			{
				PreConfig: func() {
					for _, o := range srv.vaults.List(func(o map[string]any) bool { return o["display_name"] == "team vault" }) {
						srv.vaults.Delete(o["id"].(string))
					}
				},
				Config:            cfg,
				ConfigPlanChecks:  providertest.ExpectAction(vaultAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expect(vaultAddr, map[string]knownvalue.Check{"id": knownvalue.StringExact(vaultID(3))}),
			},
			{
				PreConfig: func() {
					srv.FailNext(routeDeleteVault, http.StatusNotFound, 1)
					srv.FailNext(routeArchiveVault, http.StatusNotFound, 1)
				},
				Config:  cfg,
				Destroy: true,
			},
		},
	})

	for _, route := range []string{routeDeleteVault, routeArchiveVault} {
		if reqs := srv.Requests(route); len(reqs) != 1 || reqs[0].Fault != "status 404" {
			t.Errorf("%s: requests = %+v, want one answered 404", route, reqs)
		}
	}
	srv.AssertCalled(routeCreateVault, 3)
}

// TestVaultResource_apiErrors: an API error on create, refresh, update,
// delete or archive is an error diagnostic; none is retried (4xx).
func TestVaultResource_apiErrors(t *testing.T) {
	t.Parallel()
	archived := vaultConfig("  archive_on_destroy = true\n")
	for name, tc := range map[string]struct {
		route string
		steps func(fail func()) []resource.TestStep
	}{
		"create": {routeCreateVault, func(fail func()) []resource.TestStep {
			return []resource.TestStep{{PreConfig: fail, Config: vaultConfig(""), ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+create\s+vault:.*403\s+Forbidden`)}}
		}},
		"refresh": {routeGetVault, func(fail func()) []resource.TestStep {
			return []resource.TestStep{
				{Config: vaultConfig("")},
				{PreConfig: fail, RefreshState: true, ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+read\s+vault:.*403\s+Forbidden`)},
			}
		}},
		"update": {routeUpdateVault, func(fail func()) []resource.TestStep {
			return []resource.TestStep{
				{Config: vaultConfig("")},
				{PreConfig: fail, Config: vaultConfig("  metadata = { a = \"b\" }\n"), ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+update\s+vault:.*403\s+Forbidden`)},
			}
		}},
		"delete": {routeDeleteVault, func(fail func()) []resource.TestStep {
			return []resource.TestStep{
				{Config: vaultConfig("")},
				{PreConfig: fail, Config: vaultConfig(""), Destroy: true, ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+delete\s+vault:.*403\s+Forbidden`)},
			}
		}},
		"archive": {routeArchiveVault, func(fail func()) []resource.TestStep {
			return []resource.TestStep{
				{Config: archived},
				{PreConfig: fail, Config: archived, Destroy: true, ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+archive\s+vault:.*403\s+Forbidden`)},
			}
		}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newVaultsFake(t, 0)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(srv),
				Steps:                    tc.steps(func() { srv.FailNext(tc.route, http.StatusForbidden, 1) }),
			})
			faults := 0
			for _, r := range srv.Requests(tc.route) {
				if r.Fault != "" {
					faults++
				}
			}
			if faults != 1 {
				t.Errorf("%s: %d failed requests, want 1 (no retry)", tc.route, faults)
			}
		})
	}
}
