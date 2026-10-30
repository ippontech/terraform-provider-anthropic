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

// inviteCheck is the exact state value of the OrganizationInvite inv: a null
// accepted_at is mapped to "".
func inviteCheck(inv map[string]any) knownvalue.Check {
	checks := map[string]knownvalue.Check{}
	for _, f := range []string{"id", "email", "role", "status", "invited_at", "expires_at", "type"} {
		checks[f] = knownvalue.StringExact(fmt.Sprint(inv[f]))
	}
	accepted, _ := inv["accepted_at"].(string)
	checks["accepted_at"] = knownvalue.StringExact(accepted)
	return knownvalue.ObjectExact(checks)
}

// TestInvitesDataSource_pages lists five invites served two per page through
// the real Read, with ?beta=true on every page.
func TestInvitesDataSource_pages(t *testing.T) {
	t.Parallel()
	srv := newOrgFake(t, 2)
	var invites []map[string]any
	for i, status := range []string{"pending", "accepted", "expired", "pending", "pending"} {
		inv := inviteJSON(i+1, fmt.Sprintf("invitee%d@example.com", i+1), "developer", status)
		if status == "accepted" {
			inv["accepted_at"] = "2026-02-03T12:00:00Z"
		}
		srv.invites.Put(inv)
		invites = append(invites, inv)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.AdminKey) + `data "anthropic_invites" "test" {}`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.anthropic_invites.test", tfjsonpath.New("invites"), knownvalue.ListExact([]knownvalue.Check{
					inviteCheck(invites[0]), inviteCheck(invites[1]), inviteCheck(invites[2]), inviteCheck(invites[3]), inviteCheck(invites[4]),
				})),
			},
		}},
	})

	srv.AssertWalks(routeListInvites,
		"beta=true&limit=1000",
		"after_id="+inviteID(2)+"&beta=true&limit=1000",
		"after_id="+inviteID(4)+"&beta=true&limit=1000")
	srv.AssertAllPagesRead(routeListInvites)
	srv.assertAdminRequests(t)
}

// TestInvitesDataSource_filters sends email and each status (repeated) on
// every page.
func TestInvitesDataSource_filters(t *testing.T) {
	t.Parallel()
	srv := newOrgFake(t, 1)
	a := inviteJSON(1, "jane@example.com", "user", "pending")
	b := inviteJSON(2, "jane@example.com", "user", "expired")
	srv.invites.Put(a)
	srv.invites.Put(b)
	srv.invites.Put(inviteJSON(3, "jane@example.com", "user", "accepted"))
	srv.invites.Put(inviteJSON(4, "john@example.com", "user", "pending"))

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.AdminKey) + `
data "anthropic_invites" "test" {
  email    = "jane@example.com"
  statuses = ["pending", "expired"]
}
`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.anthropic_invites.test", tfjsonpath.New("invites"), knownvalue.ListExact([]knownvalue.Check{inviteCheck(a), inviteCheck(b)})),
				statecheck.ExpectKnownValue("data.anthropic_invites.test", tfjsonpath.New("statuses"), knownvalue.ListExact([]knownvalue.Check{
					knownvalue.StringExact("pending"), knownvalue.StringExact("expired"),
				})),
			},
		}},
	})
	const filters = "email=jane%40example.com&limit=1000&statuses=pending&statuses=expired"
	srv.AssertWalks(routeListInvites, "beta=true&"+filters, "after_id="+inviteID(1)+"&beta=true&"+filters)
	srv.AssertAllPagesRead(routeListInvites)
}

// TestInvitesDataSource_empty: no invite is an empty list.
func TestInvitesDataSource_empty(t *testing.T) {
	t.Parallel()
	srv := newOrgFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.AdminKey) + `data "anthropic_invites" "test" {}`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.anthropic_invites.test", tfjsonpath.New("invites"), knownvalue.ListExact([]knownvalue.Check{})),
			},
		}},
	})
	srv.AssertWalks(routeListInvites, "beta=true&limit=1000")
}

// TestInvitesDataSource_error: an API error is an error diagnostic.
func TestInvitesDataSource_error(t *testing.T) {
	t.Parallel()
	srv := newOrgFake(t, 0)
	srv.FailNext(routeListInvites, http.StatusForbidden, 1)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config:      providertest.ProviderConfig(sdktest.AdminKey) + `data "anthropic_invites" "test" {}`,
			ExpectError: regexp.MustCompile(`Unable\s+to\s+list\s+invites:\s+API\s+error\s+\(403\s+permission_error\)`),
		}},
	})
	srv.AssertCalled(routeListInvites, 1)
}

// TestInviteDataSource_byID maps one invite, accepted_at included.
func TestInviteDataSource_byID(t *testing.T) {
	t.Parallel()
	srv := newOrgFake(t, 0)
	inv := inviteJSON(7, "jane@example.com", "claude_code_user", "accepted")
	inv["accepted_at"] = "2026-02-09T12:00:00Z"
	srv.invites.Put(inv)
	srv.invites.Put(inviteJSON(8, "john@example.com", "user", "pending"))
	const addr = "data.anthropic_invite.test"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.AdminKey) + `data "anthropic_invite" "test" { id = "` + inviteID(7) + `" }`,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("id"), knownvalue.StringExact(inviteID(7))),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("email"), knownvalue.StringExact("jane@example.com")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("role"), knownvalue.StringExact("claude_code_user")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("status"), knownvalue.StringExact("accepted")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("invited_at"), knownvalue.StringExact("2026-02-07T09:00:00Z")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("expires_at"), knownvalue.StringExact("2026-03-07T09:00:00Z")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("accepted_at"), knownvalue.StringExact("2026-02-09T12:00:00Z")),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("type"), knownvalue.StringExact("invite")),
			},
		}},
	})
	srv.AssertWalks(routeGetInvite, "beta=true")
	for _, r := range srv.Requests(routeGetInvite) {
		if r.Path != "/v1/organizations/invites/"+inviteID(7) {
			t.Errorf("read %s, want the configured invite", r.Path)
		}
	}
	srv.assertAdminRequests(t)
}

// TestInviteDataSource_unknownID: an unknown invite is an error.
func TestInviteDataSource_unknownID(t *testing.T) {
	t.Parallel()
	srv := newOrgFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config:      providertest.ProviderConfig(sdktest.AdminKey) + `data "anthropic_invite" "test" { id = "invite_missing" }`,
			ExpectError: regexp.MustCompile(`Unable\s+to\s+read\s+invite:\s+API\s+error\s+\(404\s+not_found_error\)`),
		}},
	})
	srv.AssertCalled(routeGetInvite, 1)
}
