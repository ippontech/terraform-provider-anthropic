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

const membershipAddr = "anthropic_service_account_workspace.test"

func membershipConfig(serviceAccountID, workspaceID, role string) string {
	return providertest.ProviderConfig(sdktest.AuthToken) + fmt.Sprintf(`
resource "anthropic_service_account_workspace" "test" {
  service_account_id = %q
  workspace_id       = %q
  workspace_role     = %q
}
`, serviceAccountID, workspaceID, role)
}

func expectMembership(serviceAccountID, workspaceID, role string) []statecheck.StateCheck {
	check := func(attr string, c knownvalue.Check) statecheck.StateCheck {
		return statecheck.ExpectKnownValue(membershipAddr, tfjsonpath.New(attr), c)
	}
	return []statecheck.StateCheck{
		check("id", knownvalue.StringExact(serviceAccountID+":"+workspaceID)),
		check("service_account_id", knownvalue.StringExact(serviceAccountID)),
		check("workspace_id", knownvalue.StringExact(workspaceID)),
		check("workspace_role", knownvalue.StringExact(role)),
		check("implicit", knownvalue.Bool(false)),
		check("created_by_actor_id", knownvalue.StringExact(fakeActorID)),
	}
}

// checkNoMembership is a CheckDestroy: no explicit membership may be left.
func (f *serviceAccountsFake) checkNoMembership(*terraform.State) error {
	if n := f.memberships.Len(); n != 0 {
		return fmt.Errorf("%d memberships left after destroy", n)
	}
	return nil
}

// TestServiceAccountWorkspaceResource_lifecycle adds a service account to a
// workspace, imports the membership, changes its role in place through the
// workspace-side endpoint, replaces it on a workspace change and removes it.
func TestServiceAccountWorkspaceResource_lifecycle(t *testing.T) {
	t.Parallel()
	srv := newServiceAccountsFake(t, 0)
	sa := srv.newAccount("member", nil, nil)["id"].(string)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkNoMembership,
		Steps: []resource.TestStep{
			{
				Config:            membershipConfig(sa, "wrkspc_01", "workspace_developer"),
				ConfigPlanChecks:  expectAction(membershipAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectMembership(sa, "wrkspc_01", "workspace_developer"),
			},
			{
				Config:            membershipConfig(sa, "wrkspc_01", "workspace_developer"),
				ResourceName:      membershipAddr,
				ImportState:       true,
				ImportStateId:     sa + ":wrkspc_01",
				ImportStateVerify: true,
			},
			{
				Config:            membershipConfig(sa, "wrkspc_01", "workspace_admin"),
				ConfigPlanChecks:  expectAction(membershipAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectMembership(sa, "wrkspc_01", "workspace_admin"),
			},
			{
				Config:            membershipConfig(sa, "wrkspc_02", "workspace_admin"),
				ConfigPlanChecks:  expectAction(membershipAddr, plancheck.ResourceActionReplace),
				ConfigStateChecks: expectMembership(sa, "wrkspc_02", "workspace_admin"),
			},
		},
	})

	srv.AssertJSONBodies(routeAddMembership,
		`{"workspace_id": "wrkspc_01", "workspace_role": "workspace_developer"}`,
		`{"workspace_id": "wrkspc_02", "workspace_role": "workspace_admin"}`)
	srv.AssertJSONBodies(routeUpdateMembership, `{"workspace_role": "workspace_admin"}`)
	if got, want := srv.requestPaths(routeUpdateMembership), []string{"/v1/organizations/workspaces/wrkspc_01/service_accounts/" + sa + " "}; !slices.Equal(got, want) {
		t.Errorf("updates %q, want %q", got, want)
	}
	want := []string{
		"/v1/organizations/service_accounts/" + sa + "/workspaces/wrkspc_01 ",
		"/v1/organizations/service_accounts/" + sa + "/workspaces/wrkspc_02 ",
	}
	if got := srv.requestPaths(routeRemoveMembership); !slices.Equal(got, want) {
		t.Errorf("removals %q, want %q", got, want)
	}
	srv.assertRequestShape(t)
}

// TestServiceAccountWorkspaceResource_found on a later page: Read pages the
// membership list until it finds the tracked membership.
func TestServiceAccountWorkspaceResource_foundOnLaterPage(t *testing.T) {
	t.Parallel()
	srv := newServiceAccountsFake(t, 1)
	sa := srv.newAccount("paged", nil, nil)["id"].(string)
	srv.memberships.Put(map[string]any{
		"id": sa + ":wrkspc_other", "type": "service_account_workspace_member", "service_account_id": sa,
		"workspace_id": "wrkspc_other", "workspace_role": "workspace_user", "implicit": false, "created_by_actor_id": fakeActorID,
	})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config:            membershipConfig(sa, "wrkspc_03", "workspace_restricted_developer"),
			ConfigPlanChecks:  expectAction(membershipAddr, plancheck.ResourceActionCreate),
			ConfigStateChecks: expectMembership(sa, "wrkspc_03", "workspace_restricted_developer"),
		}},
	})

	// The implicit entry, the other membership, then the tracked one: every
	// refresh reads three pages and stops there.
	srv.AssertWalks(routeListMemberships,
		"beta=true",
		"beta=true&page="+sdktest.NextPageToken(sa+":"+defaultWorkspaceID),
		"beta=true&page="+sdktest.NextPageToken(sa+":wrkspc_other"))
	srv.assertRequestShape(t)
}

// TestServiceAccountWorkspaceResource_removedOutsideTerraform: a membership
// gone from the list leaves the state and the next apply adds it again; a
// 404 on the removal of the destroy is success.
func TestServiceAccountWorkspaceResource_removedOutsideTerraform(t *testing.T) {
	t.Parallel()
	srv := newServiceAccountsFake(t, 0)
	sa := srv.newAccount("drift", nil, nil)["id"].(string)
	config := membershipConfig(sa, "wrkspc_01", "workspace_user")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{Config: config, ConfigPlanChecks: expectAction(membershipAddr, plancheck.ResourceActionCreate)},
			{
				PreConfig: func() {
					if !srv.memberships.Delete(sa + ":wrkspc_01") {
						t.Fatalf("membership of %s in wrkspc_01 is not in the fake", sa)
					}
				},
				Config:            config,
				ConfigPlanChecks:  expectAction(membershipAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectMembership(sa, "wrkspc_01", "workspace_user"),
			},
			{
				PreConfig:        func() { srv.FailNext(routeRemoveMembership, http.StatusNotFound, 1) },
				Config:           providertest.ProviderConfig(sdktest.AuthToken),
				ConfigPlanChecks: expectAction(membershipAddr, plancheck.ResourceActionDestroy),
			},
		},
	})

	srv.AssertCalled(routeAddMembership, 2)
	if got, want := srv.requestPaths(routeRemoveMembership), []string{"/v1/organizations/service_accounts/" + sa + "/workspaces/wrkspc_01 status 404"}; !slices.Equal(got, want) {
		t.Errorf("removals %q, want %q", got, want)
	}
	srv.assertRequestShape(t)
}

// TestServiceAccountWorkspaceResource_defaultWorkspaceRevertsToImplicit:
// removing an explicit default-workspace membership reverts it to the
// implicit one, which Read must not mistake for the tracked membership.
func TestServiceAccountWorkspaceResource_defaultWorkspaceRevertsToImplicit(t *testing.T) {
	t.Parallel()
	srv := newServiceAccountsFake(t, 0)
	sa := srv.newAccount("default", nil, nil)["id"].(string)
	config := membershipConfig(sa, defaultWorkspaceID, "workspace_admin")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkNoMembership,
		Steps: []resource.TestStep{
			{
				Config:            config,
				ConfigPlanChecks:  expectAction(membershipAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectMembership(sa, defaultWorkspaceID, "workspace_admin"),
			},
			{
				PreConfig:         func() { srv.memberships.Delete(sa + ":" + defaultWorkspaceID) },
				Config:            config,
				ConfigPlanChecks:  expectAction(membershipAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectMembership(sa, defaultWorkspaceID, "workspace_admin"),
			},
		},
	})

	srv.AssertCalled(routeAddMembership, 2)
}

// TestServiceAccountWorkspaceResource_serviceAccountGone: a 404 on the
// membership list (the service account is gone) leaves the state; the add
// that follows then fails on the same 404.
func TestServiceAccountWorkspaceResource_serviceAccountGone(t *testing.T) {
	t.Parallel()
	srv := newServiceAccountsFake(t, 0)
	sa := srv.newAccount("vanishing", nil, nil)["id"].(string)
	config := membershipConfig(sa, "wrkspc_01", "workspace_user")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{Config: config, ConfigPlanChecks: expectAction(membershipAddr, plancheck.ResourceActionCreate)},
			{
				PreConfig:   func() { srv.accounts.Delete(sa) },
				Config:      config,
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+add\s+service\s+account\s+to\s+workspace:.*404`),
			},
		},
	})

	srv.AssertCalled(routeAddMembership, 2)
	// The resource left the state on the 404, so the destroy removes nothing.
	srv.AssertCalled(routeRemoveMembership, 0)
}

// TestServiceAccountWorkspaceResource_apiErrors: an API error on add,
// refresh, role update or removal reaches the operator with the provider's
// summary.
func TestServiceAccountWorkspaceResource_apiErrors(t *testing.T) {
	t.Parallel()
	srv := newServiceAccountsFake(t, 0)
	sa := srv.newAccount("errors", nil, nil)["id"].(string)
	archived := srv.newAccount("archived", nil, nil)["id"].(string)
	srv.accounts.Archive(archived)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkNoMembership,
		Steps: []resource.TestStep{
			{
				Config:      membershipConfig(archived, "wrkspc_01", "workspace_user"),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+add\s+service\s+account\s+to\s+workspace:.*400.*is\s+archived`),
			},
			{
				Config:           membershipConfig(sa, "wrkspc_01", "workspace_user"),
				ConfigPlanChecks: expectAction(membershipAddr, plancheck.ResourceActionCreate),
			},
			{
				PreConfig:   func() { srv.FailNext(routeListMemberships, http.StatusForbidden, 1) },
				Config:      membershipConfig(sa, "wrkspc_01", "workspace_user"),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+list\s+service\s+account\s+workspace\s+memberships:.*403`),
			},
			{
				PreConfig: func() { srv.FailNext(routeUpdateMembership, http.StatusBadRequest, 1) },
				Config:    membershipConfig(sa, "wrkspc_01", "workspace_admin"),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+update\s+the\s+role\s+of\s+service\s+account\s+` + sa +
					`\s+in\s+workspace\s+wrkspc_01:.*400`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeRemoveMembership, http.StatusBadRequest, 1) },
				Config:      providertest.ProviderConfig(sdktest.AuthToken),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+remove\s+service\s+account\s+from\s+workspace:.*400`),
			},
		},
	})

	srv.AssertCalled(routeAddMembership, 2)
	srv.AssertCalled(routeUpdateMembership, 1)
	// The failed removal, then the one of the final destroy.
	srv.AssertCalled(routeRemoveMembership, 2)
	srv.assertRequestShape(t)
}

// TestServiceAccountWorkspaceResource_importInvalidID: an import ID that is
// not <service_account_id>:<workspace_id> is refused before any request.
func TestServiceAccountWorkspaceResource_importInvalidID(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"svac_only", ":wrkspc_01", "svac_01:"} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			srv := newServiceAccountsFake(t, 0)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
				Steps: []resource.TestStep{{
					Config:        membershipConfig("svac_01", "wrkspc_01", "workspace_user"),
					ResourceName:  membershipAddr,
					ImportState:   true,
					ImportStateId: id,
					ExpectError:   regexp.MustCompile(`Invalid\s+Import\s+ID`),
				}},
			})
			srv.AssertCalled("", 0)
		})
	}
}
