// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces_test

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

const memberAddr = "anthropic_workspace_member.test"

func memberConfig(workspace, user, role string) string {
	return providertest.ProviderConfig(sdktest.AdminKey) + fmt.Sprintf(`
resource "anthropic_workspace_member" "test" {
  workspace_id   = %q
  user_id        = %q
  workspace_role = %q
}
`, workspace, user, role)
}

func expectMember(workspace, user, role string) []statecheck.StateCheck {
	return []statecheck.StateCheck{
		statecheck.ExpectKnownValue(memberAddr, tfjsonpath.New("id"), knownvalue.StringExact(workspace+":"+user)),
		statecheck.ExpectKnownValue(memberAddr, tfjsonpath.New("workspace_id"), knownvalue.StringExact(workspace)),
		statecheck.ExpectKnownValue(memberAddr, tfjsonpath.New("user_id"), knownvalue.StringExact(user)),
		statecheck.ExpectKnownValue(memberAddr, tfjsonpath.New("workspace_role"), knownvalue.StringExact(role)),
		statecheck.ExpectKnownValue(memberAddr, tfjsonpath.New("type"), knownvalue.StringExact("workspace_member")),
	}
}

// newMemberFake is a fake with two workspaces to add members to.
func newMemberFake(t *testing.T, pageSize int) *wsFake {
	t.Helper()
	srv := newWSFake(t, pageSize)
	srv.putWorkspace(workspaceJSON(1, "One"))
	srv.putWorkspace(workspaceJSON(2, "Two"))
	return srv
}

// TestWorkspaceMemberResource_lifecycle adds a member, imports it by
// <workspace_id>:<user_id>, changes its role in place, replaces it on a new
// user, and removes it on destroy.
func TestWorkspaceMemberResource_lifecycle(t *testing.T) {
	t.Parallel()
	srv := newMemberFake(t, 0)
	ws := workspaceID(1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy: func(*terraform.State) error {
			for _, u := range []string{"user_alice", "user_bob"} {
				if role, ok := srv.getMemberRole(ws, u); ok {
					return fmt.Errorf("%s still a member (%s) after destroy", u, role)
				}
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config:            memberConfig(ws, "user_alice", "workspace_developer"),
				ConfigPlanChecks:  providertest.ExpectAction(memberAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectMember(ws, "user_alice", "workspace_developer"),
			},
			{
				Config:            memberConfig(ws, "user_alice", "workspace_developer"),
				ResourceName:      memberAddr,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config:            memberConfig(ws, "user_alice", "workspace_admin"),
				ConfigPlanChecks:  providertest.ExpectAction(memberAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectMember(ws, "user_alice", "workspace_admin"),
			},
			{
				Config:            memberConfig(ws, "user_bob", "workspace_restricted_developer"),
				ConfigPlanChecks:  providertest.ExpectAction(memberAddr, plancheck.ResourceActionReplace),
				ConfigStateChecks: expectMember(ws, "user_bob", "workspace_restricted_developer"),
			},
		},
	})

	srv.AssertJSONBodies(routeAddMember,
		`{"user_id":"user_alice","workspace_role":"workspace_developer"}`,
		`{"user_id":"user_bob","workspace_role":"workspace_restricted_developer"}`)
	srv.AssertJSONBodies(routeUpdateMember, `{"workspace_role":"workspace_admin"}`)
	if got, want := srv.paths(routeUpdateMember), "/v1/organizations/workspaces/"+ws+"/members/user_alice"; got != want {
		t.Errorf("updated %q, want %q", got, want)
	}
	if got, want := srv.paths(routeRemoveMember), "/v1/organizations/workspaces/"+ws+"/members/user_alice, /v1/organizations/workspaces/"+ws+"/members/user_bob"; got != want {
		t.Errorf("removed %q, want %q", got, want)
	}
	srv.assertAdminRequests(t)
}

// TestWorkspaceMemberResource_moveWorkspace: a new workspace_id replaces the
// membership.
func TestWorkspaceMemberResource_moveWorkspace(t *testing.T) {
	t.Parallel()
	srv := newMemberFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{
				Config:           memberConfig(workspaceID(1), "user_alice", "workspace_user"),
				ConfigPlanChecks: providertest.ExpectAction(memberAddr, plancheck.ResourceActionCreate),
			},
			{
				Config:            memberConfig(workspaceID(2), "user_alice", "workspace_user"),
				ConfigPlanChecks:  providertest.ExpectAction(memberAddr, plancheck.ResourceActionReplace),
				ConfigStateChecks: expectMember(workspaceID(2), "user_alice", "workspace_user"),
			},
		},
	})
	if got, want := srv.paths(routeRemoveMember), "/v1/organizations/workspaces/"+workspaceID(1)+"/members/user_alice, /v1/organizations/workspaces/"+workspaceID(2)+"/members/user_alice"; got != want {
		t.Errorf("removed %q, want %q", got, want)
	}
}

// TestWorkspaceMemberResource_removedOutsideTerraform: a member removed
// behind Terraform's back leaves the state on refresh, and the next apply
// adds it again.
func TestWorkspaceMemberResource_removedOutsideTerraform(t *testing.T) {
	t.Parallel()
	srv := newMemberFake(t, 0)
	ws := workspaceID(1)
	config := memberConfig(ws, "user_alice", "workspace_user")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{Config: config, ConfigPlanChecks: providertest.ExpectAction(memberAddr, plancheck.ResourceActionCreate)},
			{
				PreConfig:         func() { srv.dropMember(ws, "user_alice") },
				Config:            config,
				ConfigPlanChecks:  providertest.ExpectAction(memberAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectMember(ws, "user_alice", "workspace_user"),
			},
		},
	})
	srv.AssertCalled(routeAddMember, 2)
	srv.AssertCalled(routeRemoveMember, 1)
}

// TestWorkspaceMemberResource_removeNotFound: a member already gone (404)
// when Terraform removes it is not an error, and the 404 is not retried.
func TestWorkspaceMemberResource_removeNotFound(t *testing.T) {
	t.Parallel()
	srv := newMemberFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{Config: memberConfig(workspaceID(1), "user_alice", "workspace_user"), ConfigPlanChecks: providertest.ExpectAction(memberAddr, plancheck.ResourceActionCreate)},
			{
				PreConfig:        func() { srv.FailNext(routeRemoveMember, http.StatusNotFound, 1) },
				Config:           providertest.ProviderConfig(sdktest.AdminKey),
				ConfigPlanChecks: providertest.ExpectAction(memberAddr, plancheck.ResourceActionDestroy),
			},
		},
	})
	srv.AssertCalled(routeRemoveMember, 1)
}

// TestWorkspaceMemberResource_apiErrors: an API error on create, refresh,
// update or delete is an error diagnostic naming the operation.
func TestWorkspaceMemberResource_apiErrors(t *testing.T) {
	t.Parallel()
	srv := newMemberFake(t, 0)
	ws := workspaceID(1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{
				// An unknown workspace is a 404 on create: an error, not a
				// silent success.
				Config:      memberConfig("wrkspc_missing", "user_alice", "workspace_user"),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+create\s+workspace\s+member:\s+API\s+error\s+\(404\s+not_found_error\)`),
			},
			{Config: memberConfig(ws, "user_alice", "workspace_user"), ConfigPlanChecks: providertest.ExpectAction(memberAddr, plancheck.ResourceActionCreate)},
			{
				PreConfig:   func() { srv.FailNext(routeGetMember, http.StatusForbidden, 1) },
				Config:      memberConfig(ws, "user_alice", "workspace_user"),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+read\s+workspace\s+member:\s+API\s+error\s+\(403\s+permission_error\)`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeUpdateMember, http.StatusBadRequest, 1) },
				Config:      memberConfig(ws, "user_alice", "workspace_admin"),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+update\s+workspace\s+member:\s+API\s+error\s+\(400\s+invalid_request_error\)`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeRemoveMember, http.StatusForbidden, 1) },
				Config:      providertest.ProviderConfig(sdktest.AdminKey),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+delete\s+workspace\s+member:\s+API\s+error\s+\(403\s+permission_error\)`),
			},
		},
	})
	srv.AssertCalled(routeAddMember, 2)
	srv.AssertCalled(routeUpdateMember, 1)
	if _, ok := srv.getMemberRole(ws, "user_alice"); ok {
		t.Error("member still present after the final destroy")
	}
}

// TestWorkspaceMemberResource_billingRoleRejected: workspace_billing fails
// the plan, before any request.
func TestWorkspaceMemberResource_billingRoleRejected(t *testing.T) {
	t.Parallel()
	srv := newMemberFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config:      memberConfig(workspaceID(1), "user_alice", "workspace_billing"),
			ExpectError: regexp.MustCompile(`workspace_billing`),
		}},
	})
	srv.AssertCalled("", 0)
}

// TestWorkspaceMemberResource_importMalformedID: an import ID that is not
// <workspace_id>:<user_id> is an error.
func TestWorkspaceMemberResource_importMalformedID(t *testing.T) {
	t.Parallel()
	for _, id := range []string{"wrkspc_only", ":user_alice", "wrkspc_x:"} {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			srv := newMemberFake(t, 0)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
				Steps: []resource.TestStep{{
					Config:        memberConfig(workspaceID(1), "user_alice", "workspace_user"),
					ResourceName:  memberAddr,
					ImportState:   true,
					ImportStateId: id,
					ExpectError:   regexp.MustCompile(`(?s)Invalid\s+import\s+ID.*<workspace_id>:<user_id>`),
				}},
			})
			srv.AssertCalled("", 0)
		})
	}
}
