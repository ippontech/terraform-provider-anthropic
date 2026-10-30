// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package serviceaccounts_test

import (
	"fmt"
	"net/http"
	"regexp"
	"slices"
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

const serviceAccountAddr = "anthropic_service_account.test"

// serviceAccountID is the ID the fake gives to the n-th service account it
// stores.
func serviceAccountID(n int) string { return fmt.Sprintf("svac_%024d", n) }

// at is the n-th tick of a fake collection's clock.
func at(n int) string { return fmt.Sprintf("2026-01-01T00:00:%02dZ", n) }

func serviceAccountConfig(name, extra string) string {
	return providertest.ProviderConfig(sdktest.AuthToken) + fmt.Sprintf(`
resource "anthropic_service_account" "test" {
  name = %q
%s}
`, name, extra)
}

// stringOrNull is the exact check of an attribute that is s, or null when s
// is nil.
func stringOrNull(s any) knownvalue.Check {
	if s == nil {
		return knownvalue.Null()
	}
	return knownvalue.StringExact(s.(string))
}

// expectServiceAccount checks every attribute of a live service account.
func expectServiceAccount(addr, id, name string, description any, role, createdAt, updatedAt string) []statecheck.StateCheck {
	check := func(attr string, c knownvalue.Check) statecheck.StateCheck {
		return statecheck.ExpectKnownValue(addr, tfjsonpath.New(attr), c)
	}
	return []statecheck.StateCheck{
		check("id", knownvalue.StringExact(id)),
		check("name", knownvalue.StringExact(name)),
		check("description", stringOrNull(description)),
		check("organization_role", knownvalue.StringExact(role)),
		check("created_at", knownvalue.StringExact(createdAt)),
		check("updated_at", knownvalue.StringExact(updatedAt)),
		check("archived_at", knownvalue.Null()),
		check("created_by_actor_id", knownvalue.StringExact(fakeActorID)),
		check("updated_by_actor_id", knownvalue.StringExact(fakeActorID)),
		check("archived_by_actor_id", knownvalue.Null()),
	}
}

func expectAction(addr string, action plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{
		PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, action)},
		PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
	}
}

// checkAllArchived is a CheckDestroy: there is no hard delete, so every
// service account left in the fake must be archived.
func (f *serviceAccountsFake) checkAllArchived(*terraform.State) error {
	for _, a := range f.accounts.List(nil) {
		if a["archived_at"] == nil {
			return fmt.Errorf("service account %s is still live after destroy", a["id"])
		}
	}
	return nil
}

// checkArchived is a CheckDestroy for the service accounts ids only.
func (f *serviceAccountsFake) checkArchived(ids ...string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		for _, id := range ids {
			if a, ok := f.accounts.Get(id); !ok || a["archived_at"] == nil {
				return fmt.Errorf("service account %s is not archived after destroy", id)
			}
		}
		return nil
	}
}

// TestServiceAccountResource_lifecycle creates a service account, imports
// it, updates its description and role in place, clears the description,
// replaces it on a name change and archives both on the way.
func TestServiceAccountResource_lifecycle(t *testing.T) {
	t.Parallel()
	srv := newServiceAccountsFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkAllArchived,
		Steps: []resource.TestStep{
			{
				Config:            serviceAccountConfig("ci-deploy", "  description = \"Deploys the app\"\n"),
				ConfigPlanChecks:  expectAction(serviceAccountAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectServiceAccount(serviceAccountAddr, serviceAccountID(1), "ci-deploy", "Deploys the app", "developer", at(1), at(1)),
			},
			{
				Config:            serviceAccountConfig("ci-deploy", "  description = \"Deploys the app\"\n"),
				ResourceName:      serviceAccountAddr,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config:            serviceAccountConfig("ci-deploy", "  description = \"Deploys every app\"\n  organization_role = \"admin\"\n"),
				ConfigPlanChecks:  expectAction(serviceAccountAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectServiceAccount(serviceAccountAddr, serviceAccountID(1), "ci-deploy", "Deploys every app", "admin", at(1), at(2)),
			},
			{
				Config:            serviceAccountConfig("ci-deploy", "  organization_role = \"admin\"\n"),
				ConfigPlanChecks:  expectAction(serviceAccountAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectServiceAccount(serviceAccountAddr, serviceAccountID(1), "ci-deploy", nil, "admin", at(1), at(3)),
			},
			{
				Config:            serviceAccountConfig("ci-deploy-v2", "  organization_role = \"admin\"\n"),
				ConfigPlanChecks:  expectAction(serviceAccountAddr, plancheck.ResourceActionReplace),
				ConfigStateChecks: expectServiceAccount(serviceAccountAddr, serviceAccountID(2), "ci-deploy-v2", nil, "admin", at(5), at(5)),
			},
		},
	})

	srv.AssertJSONBodies(routeCreateServiceAccount,
		`{"name": "ci-deploy", "description": "Deploys the app"}`,
		`{"name": "ci-deploy-v2", "organization_role": "admin"}`)
	// organization_role only when it changes: resending an unchanged admin
	// role is rejected for a workload credential. A removed description is
	// an explicit null.
	srv.AssertJSONBodies(routeUpdateServiceAccount,
		`{"description": "Deploys every app", "organization_role": "admin"}`,
		`{"description": null}`)
	want := []string{
		"/v1/organizations/service_accounts/" + serviceAccountID(1) + "/archive ",
		"/v1/organizations/service_accounts/" + serviceAccountID(2) + "/archive ",
	}
	if got := srv.requestPaths(routeArchiveServiceAccount); !slices.Equal(got, want) {
		t.Errorf("archives %q, want %q", got, want)
	}
	srv.assertRequestShape(t)
}

// TestServiceAccountResource_defaults: without description and
// organization_role the request carries the name alone and the state takes
// the API's developer role with a null description; an explicit empty
// description is sent and kept as "".
func TestServiceAccountResource_defaults(t *testing.T) {
	t.Parallel()
	srv := newServiceAccountsFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkAllArchived,
		Steps: []resource.TestStep{
			{
				Config:            serviceAccountConfig("bare", ""),
				ConfigPlanChecks:  expectAction(serviceAccountAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectServiceAccount(serviceAccountAddr, serviceAccountID(1), "bare", nil, "developer", at(1), at(1)),
			},
			{
				Config:            serviceAccountConfig("bare", "  description = \"\"\n"),
				ConfigPlanChecks:  expectAction(serviceAccountAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectServiceAccount(serviceAccountAddr, serviceAccountID(1), "bare", "", "developer", at(1), at(2)),
			},
		},
	})

	srv.AssertJSONBodies(routeCreateServiceAccount, `{"name": "bare"}`)
	srv.AssertJSONBodies(routeUpdateServiceAccount, `{"description": ""}`)
	srv.assertRequestShape(t)
}

// TestServiceAccountResource_removedOutsideTerraform: a service account the
// API no longer knows (404 on refresh) leaves the state and the next apply
// creates it again; a 404 on the archive of the destroy is success.
func TestServiceAccountResource_removedOutsideTerraform(t *testing.T) {
	t.Parallel()
	srv := newServiceAccountsFake(t, 0)
	config := serviceAccountConfig("gone", "")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{Config: config, ConfigPlanChecks: expectAction(serviceAccountAddr, plancheck.ResourceActionCreate)},
			{
				PreConfig: func() {
					if !srv.accounts.Delete(serviceAccountID(1)) {
						t.Fatalf("%s is not in the fake", serviceAccountID(1))
					}
				},
				Config:            config,
				ConfigPlanChecks:  expectAction(serviceAccountAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectServiceAccount(serviceAccountAddr, serviceAccountID(2), "gone", nil, "developer", at(2), at(2)),
			},
			{
				PreConfig:        func() { srv.FailNext(routeArchiveServiceAccount, http.StatusNotFound, 1) },
				Config:           providertest.ProviderConfig(sdktest.AuthToken),
				ConfigPlanChecks: expectAction(serviceAccountAddr, plancheck.ResourceActionDestroy),
			},
		},
	})

	srv.AssertCalled(routeCreateServiceAccount, 2)
	if got, want := srv.requestPaths(routeArchiveServiceAccount), []string{"/v1/organizations/service_accounts/" + serviceAccountID(2) + "/archive status 404"}; !slices.Equal(got, want) {
		t.Errorf("archives %q, want %q", got, want)
	}
	srv.assertRequestShape(t)
}

// TestServiceAccountResource_apiErrors: an API error on create, refresh,
// update or archive reaches the operator with the provider's summary, and
// the next run converges.
func TestServiceAccountResource_apiErrors(t *testing.T) {
	t.Parallel()
	srv := newServiceAccountsFake(t, 0)
	srv.newAccount("taken", nil, nil)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkArchived(serviceAccountID(2)),
		Steps: []resource.TestStep{
			{
				Config:      serviceAccountConfig("taken", ""),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+create\s+service\s+account:.*409.*already\s+taken`),
			},
			{
				Config:           serviceAccountConfig("errors", ""),
				ConfigPlanChecks: expectAction(serviceAccountAddr, plancheck.ResourceActionCreate),
			},
			{
				PreConfig:   func() { srv.FailNext(routeGetServiceAccount, http.StatusForbidden, 1) },
				Config:      serviceAccountConfig("errors", ""),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+read\s+service\s+account:.*403`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeUpdateServiceAccount, http.StatusBadRequest, 1) },
				Config:      serviceAccountConfig("errors", "  description = \"rejected\"\n"),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+update\s+service\s+account:.*400`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeArchiveServiceAccount, http.StatusBadRequest, 1) },
				Config:      providertest.ProviderConfig(sdktest.AuthToken),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+archive\s+service\s+account:.*400`),
			},
		},
	})

	srv.AssertCalled(routeCreateServiceAccount, 2)
	srv.AssertCalled(routeUpdateServiceAccount, 1)
	// The failed archive, then the one of the final destroy.
	srv.AssertCalled(routeArchiveServiceAccount, 2)
	srv.assertRequestShape(t)
}

// TestServiceAccountResource_importUnknown: importing an ID the API does
// not know fails instead of adopting an empty object.
func TestServiceAccountResource_importUnknown(t *testing.T) {
	t.Parallel()
	srv := newServiceAccountsFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config:        serviceAccountConfig("missing", ""),
			ResourceName:  serviceAccountAddr,
			ImportState:   true,
			ImportStateId: "svac_missing",
			ExpectError:   regexp.MustCompile(`Cannot\s+import\s+non-existent\s+remote\s+object`),
		}},
	})

	if got, want := srv.requestPaths(routeGetServiceAccount), []string{"/v1/organizations/service_accounts/svac_missing "}; !slices.Equal(got, want) {
		t.Errorf("reads %q, want %q", got, want)
	}
	srv.AssertCalled(routeCreateServiceAccount, 0)
}
