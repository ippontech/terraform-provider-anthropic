// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package organizations_test

import (
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// TestOrganizationMemberDataSource_byID maps GET
// /v1/organizations/users/{user_id} for the configured id.
func TestOrganizationMemberDataSource_byID(t *testing.T) {
	t.Parallel()
	srv := newOrgFake(t, 0)
	srv.users.Put(userJSON(1, "user"))
	srv.users.Put(userJSON(2, "membership_admin"))
	const addr = "data.anthropic_organization_member.test"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.AdminKey) + `data "anthropic_organization_member" "test" { id = "` + userID(2) + `" }`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("id"), knownvalue.StringExact(userID(2))),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("email"), knownvalue.StringExact("user2@example.com")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("name"), knownvalue.StringExact("User 2")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("role"), knownvalue.StringExact("membership_admin")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("added_at"), knownvalue.StringExact("2026-01-02T10:00:00Z")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("type"), knownvalue.StringExact("user")),
			},
		}},
	})
	for _, r := range srv.Requests(routeGetUser) {
		if r.Path != "/v1/organizations/users/"+userID(2) {
			t.Errorf("read %s, want the configured user", r.Path)
		}
	}
	srv.assertAdminRequests(t)
}

// TestOrganizationMemberDataSource_unknownID: an unknown user is an error,
// not an empty object.
func TestOrganizationMemberDataSource_unknownID(t *testing.T) {
	t.Parallel()
	srv := newOrgFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config:      providertest.ProviderConfig(sdktest.AdminKey) + `data "anthropic_organization_member" "test" { id = "user_missing" }`,
			ExpectError: regexp.MustCompile(`Unable\s+to\s+read\s+organization\s+member:\s+API\s+error\s+\(404\s+not_found_error\)`),
		}},
	})
	srv.AssertCalled(routeGetUser, 1)
}
