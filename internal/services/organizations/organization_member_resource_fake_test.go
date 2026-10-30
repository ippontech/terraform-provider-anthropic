// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package organizations_test

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

const memberAddr = "anthropic_organization_member.test"

// memberConfig manages member id with role. removable sets
// remove_on_destroy = true, without which destroy is refused and the member
// kept.
func memberConfig(id, role string, removable ...bool) string {
	remove := ""
	if len(removable) > 0 && removable[0] {
		remove = "  remove_on_destroy = true\n"
	}
	return providertest.ProviderConfig(sdktest.AdminKey) + fmt.Sprintf(`
resource "anthropic_organization_member" "test" {
  id   = %q
  role = %q
%s}
`, id, role, remove)
}

// importMember is the first step of the member tests: the resource is
// import-only, so it enters the state through terraform import.
func importMember(n int, role string) resource.TestStep {
	return resource.TestStep{
		Config:             memberConfig(userID(n), role),
		ResourceName:       memberAddr,
		ImportState:        true,
		ImportStateId:      userID(n),
		ImportStatePersist: true,
		ImportStateCheck: func(states []*terraform.InstanceState) error {
			want := userJSON(n, role)
			if len(states) != 1 {
				return fmt.Errorf("imported %d instances, want 1", len(states))
			}
			for _, f := range []string{"id", "email", "name", "role", "added_at", "type"} {
				if got := states[0].Attributes[f]; got != want[f] {
					return fmt.Errorf("imported %s = %q, want %q", f, got, want[f])
				}
			}
			return nil
		},
	}
}

func expectMember(n int, role string) []statecheck.StateCheck {
	u := userJSON(n, role)
	var checks []statecheck.StateCheck
	for _, f := range []string{"id", "email", "name", "role", "added_at", "type"} {
		checks = append(checks, statecheck.ExpectKnownValue(memberAddr, tfjsonpath.New(f), knownvalue.StringExact(fmt.Sprint(u[f]))))
	}
	return checks
}

// TestOrganizationMemberResource_lifecycle imports a member, changes its
// role in place (POST /v1/organizations/users/{id} with the role alone),
// verifies a fresh import matches, then sets remove_on_destroy (an update
// that sends the role again) and removes the member on destroy.
func TestOrganizationMemberResource_lifecycle(t *testing.T) {
	t.Parallel()
	srv := newOrgFake(t, 0)
	srv.users.Put(userJSON(1, "developer"))
	srv.users.Put(userJSON(2, "admin"))

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy: func(*terraform.State) error {
			if srv.users.Has(userID(1)) {
				return fmt.Errorf("member %s still in the organization", userID(1))
			}
			if !srv.users.Has(userID(2)) {
				return fmt.Errorf("member %s, never managed, was removed", userID(2))
			}
			return nil
		},
		Steps: []resource.TestStep{
			importMember(1, "developer"),
			{
				Config:            memberConfig(userID(1), "developer"),
				ConfigPlanChecks:  expectAction(memberAddr, plancheck.ResourceActionNoop),
				ConfigStateChecks: expectMember(1, "developer"),
			},
			{
				Config:            memberConfig(userID(1), "user"),
				ConfigPlanChecks:  expectAction(memberAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectMember(1, "user"),
			},
			{
				Config:            memberConfig(userID(1), "user"),
				ResourceName:      memberAddr,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config:           memberConfig(userID(1), "user", true),
				ConfigPlanChecks: expectAction(memberAddr, plancheck.ResourceActionUpdate),
			},
		},
	})

	srv.AssertJSONBodies(routeUpdateUser, `{"role":"user"}`, `{"role":"user"}`)
	srv.AssertCalled(routeDeleteUser, 1)
	for _, route := range []string{routeUpdateUser, routeDeleteUser} {
		for _, r := range srv.Requests(route) {
			if r.Path != "/v1/organizations/users/"+userID(1) {
				t.Errorf("%s sent to %s, want the managed member", route, r.Path)
			}
		}
	}
	srv.assertAdminRequests(t)
}

// TestOrganizationMemberResource_createAdopts: Create adopts an existing
// member whose role matches the configuration, without writing anything; a
// user who is not a member, or a member with another role, fails without a
// write.
func TestOrganizationMemberResource_createAdopts(t *testing.T) {
	t.Parallel()

	t.Run("adopted", func(t *testing.T) {
		t.Parallel()
		srv := newOrgFake(t, 0)
		srv.users.Put(userJSON(1, "developer"))
		resource.UnitTest(t, resource.TestCase{
			ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
			Steps: []resource.TestStep{{
				Config:            memberConfig(userID(1), "developer", true),
				ConfigPlanChecks:  expectAction(memberAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectMember(1, "developer"),
			}},
		})
		srv.AssertCalled(routeUpdateUser, 0)
		srv.AssertCalled(routeDeleteUser, 1)
	})

	for name, tc := range map[string]struct {
		stored  bool
		wantErr string
	}{
		"not a member":  {wantErr: `Organization\s+Member\s+Not\s+Found`},
		"role mismatch": {stored: true, wantErr: `Organization\s+Member\s+Role\s+Mismatch.*terraform\s+import`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newOrgFake(t, 0)
			if tc.stored {
				srv.users.Put(userJSON(1, "developer"))
			}
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
				Steps: []resource.TestStep{{
					Config:      memberConfig(userID(1), "user"),
					ExpectError: regexp.MustCompile(`(?s)` + tc.wantErr),
				}},
			})
			srv.AssertCalled(routeUpdateUser, 0)
			srv.AssertCalled(routeDeleteUser, 0)
		})
	}
}

// TestOrganizationMemberResource_removedOutsideTerraform: a member removed
// behind Terraform's back leaves the state on refresh; the next plan wants
// to create it, and Create, which only adopts, finds no member.
func TestOrganizationMemberResource_removedOutsideTerraform(t *testing.T) {
	t.Parallel()
	srv := newOrgFake(t, 0)
	srv.users.Put(userJSON(1, "developer"))
	srv.users.Put(userJSON(2, "billing"))

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			importMember(2, "billing"),
			{
				PreConfig: func() { srv.users.Delete(userID(2)) },
				Config:    memberConfig(userID(2), "billing"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(memberAddr, plancheck.ResourceActionCreate)},
				},
				ExpectError: regexp.MustCompile(`Organization\s+Member\s+Not\s+Found`),
			},
		},
	})
	srv.AssertCalled(routeDeleteUser, 0)
}

// TestOrganizationMemberResource_deleteNotFound: a member already gone (404)
// when Terraform removes it is not an error, and the 404 is not retried.
// remove_on_destroy is applied first: without it, destroy is refused.
func TestOrganizationMemberResource_deleteNotFound(t *testing.T) {
	t.Parallel()
	srv := newOrgFake(t, 0)
	srv.users.Put(userJSON(1, "developer"))

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			importMember(1, "developer"),
			{
				Config:           memberConfig(userID(1), "developer", true),
				ConfigPlanChecks: expectAction(memberAddr, plancheck.ResourceActionUpdate),
			},
			{
				PreConfig:        func() { srv.FailNext(routeDeleteUser, http.StatusNotFound, 1) },
				Config:           providertest.ProviderConfig(sdktest.AdminKey),
				ConfigPlanChecks: expectAction(memberAddr, plancheck.ResourceActionDestroy),
			},
		},
	})
	srv.AssertCalled(routeDeleteUser, 1)
}

// TestOrganizationMemberResource_apiErrors: an API error on update, refresh
// or delete is an error diagnostic naming the operation.
func TestOrganizationMemberResource_apiErrors(t *testing.T) {
	t.Parallel()
	srv := newOrgFake(t, 0)
	srv.users.Put(userJSON(1, "developer"))

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			importMember(1, "developer"),
			{
				// "admin cannot be assigned through the API".
				PreConfig:   func() { srv.FailNext(routeUpdateUser, http.StatusBadRequest, 1) },
				Config:      memberConfig(userID(1), "admin"),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+update\s+organization\s+member:\s+API\s+error\s+\(400\s+invalid_request_error\)`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeGetUser, http.StatusForbidden, 1) },
				Config:      memberConfig(userID(1), "developer"),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+read\s+organization\s+member:\s+API\s+error\s+\(403\s+permission_error\)`),
			},
			// Destroy is refused without remove_on_destroy, so it is applied
			// before the failed delete.
			{Config: memberConfig(userID(1), "developer", true)},
			{
				PreConfig:   func() { srv.FailNext(routeDeleteUser, http.StatusForbidden, 1) },
				Config:      providertest.ProviderConfig(sdktest.AdminKey),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+remove\s+organization\s+member:\s+API\s+error\s+\(403\s+permission_error\)`),
			},
		},
	})
	// The failed POST is not replayed: the second one sets remove_on_destroy.
	srv.AssertCalled(routeUpdateUser, 2)
	if u, _ := srv.users.Get(userID(1)); u != nil {
		t.Errorf("member still stored after the final destroy: %v", u)
	}
}
