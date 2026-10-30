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

const groupMemberAddr = "anthropic_rbac_group_member.test"

func groupMemberConfig(group, user string) string {
	return providertest.ProviderConfig(sdktest.AdminKey) + fmt.Sprintf(`
resource "anthropic_rbac_group_member" "test" {
  rbac_group_id = %q
  user_id       = %q
}
`, group, user)
}

func expectGroupMember(group, user string) []statecheck.StateCheck {
	return []statecheck.StateCheck{
		statecheck.ExpectKnownValue(groupMemberAddr, tfjsonpath.New("id"), knownvalue.StringExact(group+":"+user)),
		statecheck.ExpectKnownValue(groupMemberAddr, tfjsonpath.New("rbac_group_id"), knownvalue.StringExact(group)),
		statecheck.ExpectKnownValue(groupMemberAddr, tfjsonpath.New("user_id"), knownvalue.StringExact(user)),
		statecheck.ExpectKnownValue(groupMemberAddr, tfjsonpath.New("email"), knownvalue.StringExact(email(user))),
		statecheck.ExpectKnownValue(groupMemberAddr, tfjsonpath.New("created_at"), knownvalue.StringExact("2026-07-20T09:00:00Z")),
	}
}

// newMembersFake is a fake with two groups, the first holding two members
// already, served one member per page.
func newMembersFake(t *testing.T) *rbacFake {
	t.Helper()
	srv := newRBACFake(t, 1)
	srv.putGroup(groupJSON(1, "Engineering", "direct"))
	srv.putGroup(groupJSON(2, "Support", "direct"))
	srv.putMember(groupID(1), "user_aaron")
	srv.putMember(groupID(1), "user_bea")
	return srv
}

// TestRBACGroupMemberResource_lifecycle adds a user to a group, imports the
// membership by <rbac_group_id>:<user_id>, replaces it on a new user, and
// removes it on destroy. Every refresh pages through the members until the
// user's page, the third.
func TestRBACGroupMemberResource_lifecycle(t *testing.T) {
	t.Parallel()
	srv := newMembersFake(t)
	g := groupID(1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy: func(*terraform.State) error {
			for _, u := range []string{"user_alice", "user_bob"} {
				if srv.hasMember(g, u) {
					return fmt.Errorf("%s still in the group after destroy", u)
				}
			}
			if !srv.hasMember(g, "user_aaron") {
				return fmt.Errorf("unmanaged member user_aaron was removed")
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config:            groupMemberConfig(g, "user_alice"),
				ConfigPlanChecks:  providertest.ExpectAction(groupMemberAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectGroupMember(g, "user_alice"),
			},
			{
				Config:            groupMemberConfig(g, "user_alice"),
				ResourceName:      groupMemberAddr,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config:            groupMemberConfig(g, "user_bob"),
				ConfigPlanChecks:  providertest.ExpectAction(groupMemberAddr, plancheck.ResourceActionReplace),
				ConfigStateChecks: expectGroupMember(g, "user_bob"),
			},
		},
	})

	srv.AssertJSONBodies(routeAddGroupMember, `{"user_id":"user_alice"}`, `{"user_id":"user_bob"}`)
	var removed []string
	for _, r := range srv.Requests(routeDelGroupMember) {
		removed = append(removed, r.Path)
	}
	if want := fmt.Sprint([]string{
		"/v1/organizations/rbac_groups/" + g + "/members/user_alice", "/v1/organizations/rbac_groups/" + g + "/members/user_bob",
	}); fmt.Sprint(removed) != want {
		t.Errorf("removed %v, want %s", removed, want)
	}
	srv.assertRBACRequests(t)
}

// TestRBACGroupMemberResource_stopsAtTheUsersPage: the refresh stops paging
// once it has found the user.
func TestRBACGroupMemberResource_stopsAtTheUsersPage(t *testing.T) {
	t.Parallel()
	srv := newMembersFake(t)
	g := groupID(1)
	srv.putMember(g, "user_alice")
	srv.putMember(g, "user_zoe")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{
				Config:             groupMemberConfig(g, "user_alice"),
				ResourceName:       groupMemberAddr,
				ImportState:        true,
				ImportStateId:      g + ":user_alice",
				ImportStatePersist: true,
			},
			{
				Config:            groupMemberConfig(g, "user_alice"),
				ConfigPlanChecks:  providertest.ExpectAction(groupMemberAddr, plancheck.ResourceActionNoop),
				ConfigStateChecks: expectGroupMember(g, "user_alice"),
			},
		},
	})
	// Three pages of one member each (aaron, bea, alice), never zoe's.
	srv.AssertWalks(routeListGroupMember,
		"beta=true&limit=1000",
		"beta=true&limit=1000&page="+sdktest.NextPageToken("user_aaron"),
		"beta=true&limit=1000&page="+sdktest.NextPageToken("user_bea"))
}

// TestRBACGroupMemberResource_goneOutsideTerraform: a membership removed
// behind Terraform's back, or a group deleted, leaves the state on refresh;
// the next apply adds the user again, which fails for a missing group.
func TestRBACGroupMemberResource_goneOutsideTerraform(t *testing.T) {
	t.Parallel()
	srv := newMembersFake(t)
	g := groupID(2)
	config := groupMemberConfig(g, "user_alice")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{Config: config, ConfigPlanChecks: providertest.ExpectAction(groupMemberAddr, plancheck.ResourceActionCreate)},
			{
				PreConfig:         func() { srv.dropMember(g, "user_alice") },
				Config:            config,
				ConfigPlanChecks:  providertest.ExpectAction(groupMemberAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectGroupMember(g, "user_alice"),
			},
			{
				PreConfig: func() { srv.dropGroup(g) },
				Config:    config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(groupMemberAddr, plancheck.ResourceActionCreate)},
				},
				ExpectError: regexp.MustCompile(`Unable\s+to\s+add\s+user\s+to\s+RBAC\s+group:\s+API\s+error\s+\(404\s+not_found_error\)`),
			},
		},
	})
	srv.AssertCalled(routeAddGroupMember, 3)
	srv.AssertCalled(routeDelGroupMember, 0)
}

// TestRBACGroupMemberResource_removeNotFound: a membership already gone (404)
// when Terraform removes it is not an error.
func TestRBACGroupMemberResource_removeNotFound(t *testing.T) {
	t.Parallel()
	srv := newMembersFake(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{Config: groupMemberConfig(groupID(2), "user_alice"), ConfigPlanChecks: providertest.ExpectAction(groupMemberAddr, plancheck.ResourceActionCreate)},
			{
				PreConfig:        func() { srv.FailNext(routeDelGroupMember, http.StatusNotFound, 1) },
				Config:           providertest.ProviderConfig(sdktest.AdminKey),
				ConfigPlanChecks: providertest.ExpectAction(groupMemberAddr, plancheck.ResourceActionDestroy),
			},
		},
	})
	srv.AssertCalled(routeDelGroupMember, 1)
}

// TestRBACGroupMemberResource_apiErrors: an API error on the member list or
// on the removal is an error diagnostic naming the operation; an import ID
// that is not <rbac_group_id>:<user_id> is refused.
func TestRBACGroupMemberResource_apiErrors(t *testing.T) {
	t.Parallel()
	srv := newMembersFake(t)
	config := groupMemberConfig(groupID(2), "user_alice")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{
				Config:        config,
				ResourceName:  groupMemberAddr,
				ImportState:   true,
				ImportStateId: groupID(2),
				ExpectError:   regexp.MustCompile(`(?s)Invalid\s+Import\s+ID.*<rbac_group_id>:<user_id>`),
			},
			{Config: config, ConfigPlanChecks: providertest.ExpectAction(groupMemberAddr, plancheck.ResourceActionCreate)},
			{
				PreConfig:   func() { srv.FailNext(routeListGroupMember, http.StatusForbidden, 1) },
				Config:      config,
				ExpectError: regexp.MustCompile(`Unable\s+to\s+list\s+RBAC\s+group\s+members:\s+API\s+error\s+\(403\s+permission_error\)`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeDelGroupMember, http.StatusForbidden, 1) },
				Config:      providertest.ProviderConfig(sdktest.AdminKey),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+remove\s+user\s+from\s+RBAC\s+group:\s+API\s+error\s+\(403\s+permission_error\)`),
			},
		},
	})
	if srv.hasMember(groupID(2), "user_alice") {
		t.Error("membership still present after the final destroy")
	}
}
