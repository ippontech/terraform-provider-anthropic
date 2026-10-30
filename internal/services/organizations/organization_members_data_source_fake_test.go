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
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

func memberCheck(n int, role string) knownvalue.Check {
	return userCheck(userJSON(n, role))
}

// userCheck is the exact state value of the OrganizationUser u.
func userCheck(u map[string]any) knownvalue.Check {
	checks := map[string]knownvalue.Check{}
	for _, f := range []string{"id", "email", "name", "role", "added_at", "type"} {
		checks[f] = knownvalue.StringExact(fmt.Sprint(u[f]))
	}
	return knownvalue.ObjectExact(checks)
}

// TestOrganizationMembersDataSource_pages lists five members served two per
// page through the real Read: it follows last_id with after_id until
// has_more is false, keeps the API's order and asks for the largest page.
func TestOrganizationMembersDataSource_pages(t *testing.T) {
	t.Parallel()
	srv := newOrgFake(t, 2)
	roles := []string{"admin", "developer", "user", "billing", "claude_code_user"}
	for i, role := range roles {
		srv.users.Put(userJSON(i+1, role))
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.AdminKey) + `data "anthropic_organization_members" "test" {}`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.anthropic_organization_members.test", tfjsonpath.New("members"), knownvalue.ListExact([]knownvalue.Check{
					memberCheck(1, "admin"), memberCheck(2, "developer"), memberCheck(3, "user"),
					memberCheck(4, "billing"), memberCheck(5, "claude_code_user"),
				})),
				statecheck.ExpectKnownValue("data.anthropic_organization_members.test", tfjsonpath.New("email"), knownvalue.Null()),
			},
		}},
	})

	srv.AssertWalks(routeListUsers, "limit=1000", "after_id="+userID(2)+"&limit=1000", "after_id="+userID(4)+"&limit=1000")
	srv.AssertAllPagesRead(routeListUsers)
	srv.assertAdminRequests(t)
}

// TestOrganizationMembersDataSource_emailFilter sends the email filter on
// every page and keeps it in state.
func TestOrganizationMembersDataSource_emailFilter(t *testing.T) {
	t.Parallel()
	srv := newOrgFake(t, 1)
	srv.users.Put(userJSON(1, "user"))
	srv.users.Put(userJSON(2, "developer"))
	second := userJSON(3, "billing")
	second["email"] = "user2@example.com" // a second member with the same email, so the filter pages
	srv.users.Put(second)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.AdminKey) + `data "anthropic_organization_members" "test" { email = "user2@example.com" }`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.anthropic_organization_members.test", tfjsonpath.New("members"), knownvalue.ListExact([]knownvalue.Check{
					memberCheck(2, "developer"),
					userCheck(second),
				})),
				statecheck.ExpectKnownValue("data.anthropic_organization_members.test", tfjsonpath.New("email"), knownvalue.StringExact("user2@example.com")),
			},
		}},
	})
	srv.AssertWalks(routeListUsers, "email=user2%40example.com&limit=1000", "after_id="+userID(2)+"&email=user2%40example.com&limit=1000")
	srv.AssertAllPagesRead(routeListUsers)
}

// TestOrganizationMembersDataSource_empty: no member is an empty list.
func TestOrganizationMembersDataSource_empty(t *testing.T) {
	t.Parallel()
	srv := newOrgFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.AdminKey) + `data "anthropic_organization_members" "test" { email = "nobody@example.com" }`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.anthropic_organization_members.test", tfjsonpath.New("members"), knownvalue.ListExact([]knownvalue.Check{})),
			},
		}},
	})
	srv.AssertWalks(routeListUsers, "email=nobody%40example.com&limit=1000")
}

// TestOrganizationMembersDataSource_error: an API error is an error
// diagnostic with the API's message, not a short list.
func TestOrganizationMembersDataSource_error(t *testing.T) {
	t.Parallel()
	srv := newOrgFake(t, 1)
	srv.users.Put(userJSON(1, "user"))
	srv.FailNext(routeListUsers, http.StatusForbidden, 1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config:      providertest.ProviderConfig(sdktest.AdminKey) + `data "anthropic_organization_members" "test" {}`,
			ExpectError: regexp.MustCompile(`Unable\s+to\s+list\s+organization\s+members:\s+API\s+error\s+\(403\s+permission_error\)`),
		}},
	})
	// A 403 is not retried.
	srv.AssertCalled(routeListUsers, 1)
}
