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
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

func memberObject(workspace, user, role string) knownvalue.Check {
	return knownvalue.ObjectExact(map[string]knownvalue.Check{
		"user_id":        knownvalue.StringExact(user),
		"workspace_id":   knownvalue.StringExact(workspace),
		"workspace_role": knownvalue.StringExact(role),
		"type":           knownvalue.StringExact("workspace_member"),
	})
}

// TestWorkspaceMembersDataSource_pages lists the five members of a workspace,
// two per page, through the real Read: it follows last_id (the user ID) with
// after_id until has_more is false, and leaves other workspaces out.
func TestWorkspaceMembersDataSource_pages(t *testing.T) {
	t.Parallel()
	srv := newMemberFake(t, 2)
	ws := workspaceID(1)
	roles := []string{"workspace_admin", "workspace_developer", "workspace_user", "workspace_billing", "workspace_restricted_developer"}
	var want []knownvalue.Check
	for i, role := range roles {
		user := fmt.Sprintf("user_%02d", i+1)
		srv.putMember(memberJSON(ws, user, role))
		srv.putMember(memberJSON(workspaceID(2), user+"_other", "workspace_user"))
		want = append(want, memberObject(ws, user, role))
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.AdminKey) + `data "anthropic_workspace_members" "test" { workspace_id = "` + ws + `" }`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.anthropic_workspace_members.test", tfjsonpath.New("members"), knownvalue.ListExact(want)),
			},
		}},
	})
	srv.AssertWalks(routeListMembers, "limit=1000", "after_id=user_02&limit=1000", "after_id=user_04&limit=1000")
	srv.AssertAllPagesRead(routeListMembers)
	for _, r := range srv.Requests(routeListMembers) {
		if r.Path != "/v1/organizations/workspaces/"+ws+"/members" {
			t.Errorf("listed %s, want the configured workspace", r.Path)
		}
	}
	srv.assertAdminRequests(t)
}

// TestWorkspaceMembersDataSource_empty: a workspace without members is an
// empty list.
func TestWorkspaceMembersDataSource_empty(t *testing.T) {
	t.Parallel()
	srv := newMemberFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.AdminKey) + `data "anthropic_workspace_members" "test" { workspace_id = "` + workspaceID(2) + `" }`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.anthropic_workspace_members.test", tfjsonpath.New("members"), knownvalue.ListExact([]knownvalue.Check{})),
			},
		}},
	})
	srv.AssertWalks(routeListMembers, "limit=1000")
}

// TestWorkspaceMembersDataSource_error: an API error is an error diagnostic.
func TestWorkspaceMembersDataSource_error(t *testing.T) {
	t.Parallel()
	srv := newMemberFake(t, 0)
	srv.FailNext(routeListMembers, http.StatusNotFound, 1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config:      providertest.ProviderConfig(sdktest.AdminKey) + `data "anthropic_workspace_members" "test" { workspace_id = "wrkspc_missing" }`,
			ExpectError: regexp.MustCompile(`Unable\s+to\s+list\s+workspace\s+members:\s+API\s+error\s+\(404\s+not_found_error\)`),
		}},
	})
	srv.AssertCalled(routeListMembers, 1)
}

// TestWorkspaceMemberDataSource_byID reads one member by workspace and user.
func TestWorkspaceMemberDataSource_byID(t *testing.T) {
	t.Parallel()
	srv := newMemberFake(t, 0)
	srv.putMember(memberJSON(workspaceID(1), "user_alice", "workspace_user"))
	srv.putMember(memberJSON(workspaceID(2), "user_alice", "workspace_billing"))
	const addr = "data.anthropic_workspace_member.test"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.AdminKey) + `
data "anthropic_workspace_member" "test" {
  workspace_id = "` + workspaceID(2) + `"
  user_id      = "user_alice"
}
`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("workspace_id"), knownvalue.StringExact(workspaceID(2))),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("user_id"), knownvalue.StringExact("user_alice")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("workspace_role"), knownvalue.StringExact("workspace_billing")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("type"), knownvalue.StringExact("workspace_member")),
			},
		}},
	})
	for _, r := range srv.Requests(routeGetMember) {
		if r.Path != "/v1/organizations/workspaces/"+workspaceID(2)+"/members/user_alice" {
			t.Errorf("read %s, want the configured member", r.Path)
		}
	}
	srv.assertAdminRequests(t)
}

// TestWorkspaceMemberDataSource_unknownMember: a user who is not a member is
// an error.
func TestWorkspaceMemberDataSource_unknownMember(t *testing.T) {
	t.Parallel()
	srv := newMemberFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.AdminKey) + `
data "anthropic_workspace_member" "test" {
  workspace_id = "` + workspaceID(1) + `"
  user_id      = "user_nobody"
}
`,
			ExpectError: regexp.MustCompile(`Unable\s+to\s+read\s+workspace\s+member:\s+API\s+error\s+\(404\s+not_found_error\)`),
		}},
	})
	srv.AssertCalled(routeGetMember, 1)
}
