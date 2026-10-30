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
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

func groupObject(n int, name, source string, roleIDs knownvalue.Check) knownvalue.Check {
	created := fmt.Sprintf("2026-07-%02dT08:00:00Z", n)
	return knownvalue.ObjectExact(map[string]knownvalue.Check{
		"id": knownvalue.StringExact(groupID(n)), "name": knownvalue.StringExact(name), "source_type": knownvalue.StringExact(source),
		"role_ids": roleIDs, "created_at": knownvalue.StringExact(created), "updated_at": knownvalue.StringExact(created),
	})
}

// TestRBACGroupsDataSource_pages lists three groups, two per page, through
// the real Read: it follows next_page while has_more is true.
func TestRBACGroupsDataSource_pages(t *testing.T) {
	t.Parallel()
	srv := newRBACFake(t, 2)
	srv.putGroup(groupJSON(1, "Engineering", "direct", "rbac_role_a"))
	srv.putGroup(groupJSON(2, "IdP", "scim"))
	unavailable := groupJSON(3, "Pending", "direct")
	unavailable["role_ids"], unavailable["roles"] = nil, nil
	srv.putGroup(unavailable)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.AdminKey) + `data "anthropic_rbac_groups" "test" {}`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.anthropic_rbac_groups.test", tfjsonpath.New("rbac_groups"), knownvalue.ListExact([]knownvalue.Check{
					groupObject(1, "Engineering", "direct", roleIDsCheck("rbac_role_a")),
					groupObject(2, "IdP", "scim", roleIDsCheck()),
					groupObject(3, "Pending", "direct", knownvalue.Null()),
				})),
			},
		}},
	})
	srv.AssertWalks(routeListGroups, "beta=true&limit=1000", "beta=true&limit=1000&page="+sdktest.NextPageToken(groupID(2)))
	srv.AssertAllPagesRead(routeListGroups)
	srv.assertRBACRequests(t)
}

// TestRBACGroupsDataSource_emptyAndError: no group is an empty list; an API
// error is an error diagnostic.
func TestRBACGroupsDataSource_emptyAndError(t *testing.T) {
	t.Parallel()
	srv := newRBACFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{
				Config: providertest.ProviderConfig(sdktest.AdminKey) + `data "anthropic_rbac_groups" "test" {}`,
				ConfigStateChecks: []statecheck.StateCheck{
					statecheck.ExpectKnownValue("data.anthropic_rbac_groups.test", tfjsonpath.New("rbac_groups"), knownvalue.ListExact([]knownvalue.Check{})),
				},
			},
			{
				PreConfig:   func() { srv.FailNext(routeListGroups, http.StatusForbidden, 1) },
				Config:      providertest.ProviderConfig(sdktest.AdminKey) + `data "anthropic_rbac_groups" "test" {}`,
				ExpectError: regexp.MustCompile(`Unable\s+to\s+list\s+RBAC\s+groups:\s+API\s+error\s+\(403\s+permission_error\)`),
			},
		},
	})
}

// TestRBACGroupDataSource_byID reads one group; role_ids falls back on the
// deprecated roles when the answer has no role_ids.
func TestRBACGroupDataSource_byID(t *testing.T) {
	t.Parallel()
	srv := newRBACFake(t, 0)
	srv.putGroup(groupJSON(1, "Other", "direct"))
	legacy := groupJSON(2, "Legacy", "direct", "rbac_role_x")
	delete(legacy, "role_ids")
	srv.putGroup(legacy)
	const addr = "data.anthropic_rbac_group.test"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.AdminKey) + `data "anthropic_rbac_group" "test" { id = "` + groupID(2) + `" }`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("id"), knownvalue.StringExact(groupID(2))),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("name"), knownvalue.StringExact("Legacy")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("source_type"), knownvalue.StringExact("direct")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("role_ids"), roleIDsCheck("rbac_role_x")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("created_at"), knownvalue.StringExact("2026-07-02T08:00:00Z")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("updated_at"), knownvalue.StringExact("2026-07-02T08:00:00Z")),
			},
		}},
	})
	srv.AssertWalks(routeGetGroup, "beta=true")
	srv.assertRBACRequests(t)
}

// TestRBACGroupDataSource_errors: an unknown group is "RBAC Group Not
// Found"; another API error names the read.
func TestRBACGroupDataSource_errors(t *testing.T) {
	t.Parallel()
	srv := newRBACFake(t, 0)
	srv.putGroup(groupJSON(1, "Engineering", "direct"))

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{
				Config:      providertest.ProviderConfig(sdktest.AdminKey) + `data "anthropic_rbac_group" "test" { id = "rbac_group_missing" }`,
				ExpectError: regexp.MustCompile(`(?s)RBAC\s+Group\s+Not\s+Found.*"rbac_group_missing"`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeGetGroup, http.StatusForbidden, 1) },
				Config:      providertest.ProviderConfig(sdktest.AdminKey) + `data "anthropic_rbac_group" "test" { id = "` + groupID(1) + `" }`,
				ExpectError: regexp.MustCompile(`Unable\s+to\s+read\s+RBAC\s+group:\s+API\s+error\s+\(403\s+permission_error\)`),
			},
		},
	})
	srv.AssertCalled(routeGetGroup, 2)
}
