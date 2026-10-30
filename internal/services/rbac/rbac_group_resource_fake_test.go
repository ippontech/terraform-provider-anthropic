// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package rbac_test

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

const groupAddr = "anthropic_rbac_group.test"

func groupConfig(name string) string {
	return providertest.ProviderConfig(sdktest.AdminKey) + fmt.Sprintf("resource \"anthropic_rbac_group\" \"test\" {\n  name = %q\n}\n", name)
}

func roleIDsCheck(ids ...string) knownvalue.Check {
	checks := make([]knownvalue.Check, len(ids))
	for i, id := range ids {
		checks[i] = knownvalue.StringExact(id)
	}
	return knownvalue.SetExact(checks)
}

func expectGroup(n int, name, source string, roleIDs knownvalue.Check, updatedAt string) []statecheck.StateCheck {
	at := func(attr string, c knownvalue.Check) statecheck.StateCheck {
		return statecheck.ExpectKnownValue(groupAddr, tfjsonpath.New(attr), c)
	}
	return []statecheck.StateCheck{
		at("id", knownvalue.StringExact(groupID(n))),
		at("name", knownvalue.StringExact(name)),
		at("source_type", knownvalue.StringExact(source)),
		at("role_ids", roleIDs),
		at("created_at", knownvalue.StringExact(fmt.Sprintf("2026-07-%02dT08:00:00Z", n))),
		at("updated_at", knownvalue.StringExact(updatedAt)),
	}
}

func (f *rbacFake) checkNoGroup(ids ...string) func(*terraform.State) error {
	return func(*terraform.State) error {
		for _, id := range ids {
			if f.group(id) != nil {
				return fmt.Errorf("RBAC group %s still exists after destroy", id)
			}
		}
		return nil
	}
}

// setRoles attaches roles to a group, as the admin settings would (role_ids
// is read-only on the API); nil stands for the JSON null of "role data
// temporarily unavailable".
func (f *rbacFake) setRoles(id string, roleIDs []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	g := f.groups[find(f.groups, id)]
	g["role_ids"], g["roles"] = roleIDs, roleIDs
}

// TestRBACGroupResource_lifecycle creates a group, imports it, renames it in
// place, follows roles attached outside Terraform (read-only, no diff),
// and deletes it on destroy.
func TestRBACGroupResource_lifecycle(t *testing.T) {
	t.Parallel()
	srv := newRBACFake(t, 0)
	created := fmt.Sprintf("2026-07-%02dT08:00:00Z", 1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkNoGroup(groupID(1)),
		Steps: []resource.TestStep{
			{
				Config:            groupConfig("Engineering"),
				ConfigPlanChecks:  expectAction(groupAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectGroup(1, "Engineering", "direct", roleIDsCheck(), created),
			},
			{
				Config:            groupConfig("Engineering"),
				ResourceName:      groupAddr,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config:            groupConfig("Platform"),
				ConfigPlanChecks:  expectAction(groupAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectGroup(1, "Platform", "direct", roleIDsCheck(), "2026-08-01T00:00:00Z"),
			},
			{
				PreConfig:         func() { srv.setRoles(groupID(1), []string{"rbac_role_a", "rbac_role_b"}) },
				Config:            groupConfig("Platform"),
				ConfigPlanChecks:  expectAction(groupAddr, plancheck.ResourceActionNoop),
				ConfigStateChecks: expectGroup(1, "Platform", "direct", roleIDsCheck("rbac_role_a", "rbac_role_b"), "2026-08-01T00:00:00Z"),
			},
			{
				PreConfig:         func() { srv.setRoles(groupID(1), nil) },
				Config:            groupConfig("Platform"),
				ConfigPlanChecks:  expectAction(groupAddr, plancheck.ResourceActionNoop),
				ConfigStateChecks: expectGroup(1, "Platform", "direct", knownvalue.Null(), "2026-08-01T00:00:00Z"),
			},
		},
	})

	srv.AssertJSONBodies(routeCreateGroup, `{"name":"Engineering"}`)
	srv.AssertJSONBodies(routeUpdateGroup, `{"name":"Platform"}`)
	srv.AssertCalled(routeDeleteGroup, 1)
	srv.assertRBACRequests(t)
}

// TestRBACGroupResource_removedOutsideTerraform: a group deleted behind
// Terraform's back leaves the state on refresh, and the next apply creates
// it again.
func TestRBACGroupResource_removedOutsideTerraform(t *testing.T) {
	t.Parallel()
	srv := newRBACFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkNoGroup(groupID(2)),
		Steps: []resource.TestStep{
			{Config: groupConfig("Ops"), ConfigPlanChecks: expectAction(groupAddr, plancheck.ResourceActionCreate)},
			{
				PreConfig:         func() { srv.dropGroup(groupID(1)) },
				Config:            groupConfig("Ops"),
				ConfigPlanChecks:  expectAction(groupAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectGroup(2, "Ops", "direct", roleIDsCheck(), fmt.Sprintf("2026-07-%02dT08:00:00Z", 2)),
			},
		},
	})
	srv.AssertCalled(routeCreateGroup, 2)
}

// TestRBACGroupResource_deleteNotFound: a group already gone (404) when
// Terraform deletes it is not an error, and the 404 is not retried.
func TestRBACGroupResource_deleteNotFound(t *testing.T) {
	t.Parallel()
	srv := newRBACFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{Config: groupConfig("Racing"), ConfigPlanChecks: expectAction(groupAddr, plancheck.ResourceActionCreate)},
			{
				PreConfig:        func() { srv.FailNext(routeDeleteGroup, http.StatusNotFound, 1) },
				Config:           providertest.ProviderConfig(sdktest.AdminKey),
				ConfigPlanChecks: expectAction(groupAddr, plancheck.ResourceActionDestroy),
			},
		},
	})
	srv.AssertCalled(routeDeleteGroup, 1)
}

// TestRBACGroupResource_scimGroupRename: renaming an imported SCIM group is
// planned (with a warning) and the API's refusal reaches the operator.
func TestRBACGroupResource_scimGroupRename(t *testing.T) {
	t.Parallel()
	srv := newRBACFake(t, 0)
	srv.putGroup(groupJSON(1, "IdP group", "scim", "rbac_role_a"))

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{
				Config:             groupConfig("IdP group"),
				ResourceName:       groupAddr,
				ImportState:        true,
				ImportStateId:      groupID(1),
				ImportStatePersist: true,
			},
			{
				Config:            groupConfig("IdP group"),
				ConfigPlanChecks:  expectAction(groupAddr, plancheck.ResourceActionNoop),
				ConfigStateChecks: expectGroup(1, "IdP group", "scim", roleIDsCheck("rbac_role_a"), fmt.Sprintf("2026-07-%02dT08:00:00Z", 1)),
			},
			{
				Config: groupConfig("Renamed"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(groupAddr, plancheck.ResourceActionUpdate)},
				},
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+update\s+RBAC\s+group:.*SCIM-provisioned`),
			},
			{
				// Once the identity provider has removed it, as the warning
				// advises, the destroy has nothing left to delete.
				PreConfig: func() { srv.dropGroup(groupID(1)) },
				Config:    groupConfig("IdP group"),
				Destroy:   true,
			},
		},
	})
	srv.AssertCalled(routeUpdateGroup, 1)
	srv.AssertCalled(routeDeleteGroup, 0)
}

// TestRBACGroupResource_apiErrors: an API error on create, refresh, update or
// delete is an error diagnostic naming the operation.
func TestRBACGroupResource_apiErrors(t *testing.T) {
	t.Parallel()
	srv := newRBACFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkNoGroup(groupID(1)),
		Steps: []resource.TestStep{
			{
				PreConfig:   func() { srv.FailNext(routeCreateGroup, http.StatusForbidden, 1) },
				Config:      groupConfig("Faulty"),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+create\s+RBAC\s+group:\s+API\s+error\s+\(403\s+permission_error\)`),
			},
			{Config: groupConfig("Faulty"), ConfigPlanChecks: expectAction(groupAddr, plancheck.ResourceActionCreate)},
			{
				PreConfig:   func() { srv.FailNext(routeGetGroup, http.StatusForbidden, 1) },
				Config:      groupConfig("Faulty"),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+read\s+RBAC\s+group:\s+API\s+error\s+\(403\s+permission_error\)`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeUpdateGroup, http.StatusBadRequest, 1) },
				Config:      groupConfig("Renamed"),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+update\s+RBAC\s+group:\s+API\s+error\s+\(400\s+invalid_request_error\)`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeDeleteGroup, http.StatusForbidden, 1) },
				Config:      providertest.ProviderConfig(sdktest.AdminKey),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+delete\s+RBAC\s+group:\s+API\s+error\s+\(403\s+permission_error\)`),
			},
		},
	})
	srv.AssertCalled(routeCreateGroup, 2)
	srv.AssertCalled(routeUpdateGroup, 1)
	srv.AssertCalled(routeDeleteGroup, 2)
}
