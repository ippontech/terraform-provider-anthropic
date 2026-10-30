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

const inviteAddr = "anthropic_invite.test"

func inviteConfig(email, role string) string {
	return providertest.ProviderConfig(sdktest.AdminKey) + fmt.Sprintf(`
resource "anthropic_invite" "test" {
  email = %q
  role  = %q
}
`, email, role)
}

// expectInvite is the state of the n-th invite the fake created.
func expectInvite(n int, email, role string) []statecheck.StateCheck {
	return []statecheck.StateCheck{
		statecheck.ExpectKnownValue(inviteAddr, tfjsonpath.New("id"), knownvalue.StringExact(inviteID(n))),
		statecheck.ExpectKnownValue(inviteAddr, tfjsonpath.New("email"), knownvalue.StringExact(email)),
		statecheck.ExpectKnownValue(inviteAddr, tfjsonpath.New("role"), knownvalue.StringExact(role)),
		statecheck.ExpectKnownValue(inviteAddr, tfjsonpath.New("status"), knownvalue.StringExact("pending")),
		statecheck.ExpectKnownValue(inviteAddr, tfjsonpath.New("invited_at"), knownvalue.StringExact(fmt.Sprintf("2026-02-%02dT09:00:00Z", n))),
		statecheck.ExpectKnownValue(inviteAddr, tfjsonpath.New("expires_at"), knownvalue.StringExact(fmt.Sprintf("2026-03-%02dT09:00:00Z", n))),
		// accepted_at is null in the API answer, "" in state.
		statecheck.ExpectKnownValue(inviteAddr, tfjsonpath.New("accepted_at"), knownvalue.StringExact("")),
		statecheck.ExpectKnownValue(inviteAddr, tfjsonpath.New("type"), knownvalue.StringExact("invite")),
	}
}

// checkNoPendingInvite is the CheckDestroy of the invite tests: destroying
// cancels (deletes) every pending invite Terraform created.
func (f *orgFake) checkNoPendingInvite(*terraform.State) error {
	for _, inv := range f.invites.List(nil) {
		if inv["status"] == "pending" {
			return fmt.Errorf("invite %s is still pending after destroy", inv["id"])
		}
	}
	return nil
}

// TestInviteResource_lifecycle creates an invite, imports it, replaces it on
// a role change (no update endpoint: email and role are RequiresReplace) and
// destroys it.
func TestInviteResource_lifecycle(t *testing.T) {
	t.Parallel()
	srv := newOrgFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkNoPendingInvite,
		Steps: []resource.TestStep{
			{
				Config:            inviteConfig("jane@example.com", "developer"),
				ConfigPlanChecks:  providertest.ExpectAction(inviteAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectInvite(1, "jane@example.com", "developer"),
			},
			{
				Config:            inviteConfig("jane@example.com", "developer"),
				ResourceName:      inviteAddr,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config:            inviteConfig("jane@example.com", "user"),
				ConfigPlanChecks:  providertest.ExpectAction(inviteAddr, plancheck.ResourceActionReplace),
				ConfigStateChecks: expectInvite(2, "jane@example.com", "user"),
			},
			{
				Config:            inviteConfig("john@example.com", "user"),
				ConfigPlanChecks:  providertest.ExpectAction(inviteAddr, plancheck.ResourceActionReplace),
				ConfigStateChecks: expectInvite(3, "john@example.com", "user"),
			},
		},
	})

	srv.AssertJSONBodies(routeCreateInvite,
		`{"email":"jane@example.com","role":"developer"}`,
		`{"email":"jane@example.com","role":"user"}`,
		`{"email":"john@example.com","role":"user"}`)
	srv.AssertQueries(routeCreateInvite, "beta=true", "beta=true", "beta=true")
	srv.AssertQueries(routeDeleteInvite, "beta=true", "beta=true", "beta=true")
	var deleted []string
	for _, r := range srv.Requests(routeDeleteInvite) {
		deleted = append(deleted, r.Path)
	}
	if want := fmt.Sprint([]string{
		"/v1/organizations/invites/" + inviteID(1), "/v1/organizations/invites/" + inviteID(2), "/v1/organizations/invites/" + inviteID(3),
	}); fmt.Sprint(deleted) != want {
		t.Errorf("deleted %v, want %s", deleted, want)
	}
	srv.AssertWalks(routeGetInvite, "beta=true")
	srv.assertAdminRequests(t)
}

// TestInviteResource_completedOrGone: an invite that is no longer pending
// (accepted, expired) stays in state with its new status and is not sent
// again; one deleted behind Terraform's back leaves the state on refresh and
// the next apply invites again. Destroy cancels only a pending invite.
func TestInviteResource_completedOrGone(t *testing.T) {
	t.Parallel()
	srv := newOrgFake(t, 0)
	config := inviteConfig("jane@example.com", "developer")
	status := func(n int, want string) []statecheck.StateCheck {
		return []statecheck.StateCheck{
			statecheck.ExpectKnownValue(inviteAddr, tfjsonpath.New("id"), knownvalue.StringExact(inviteID(n))),
			statecheck.ExpectKnownValue(inviteAddr, tfjsonpath.New("status"), knownvalue.StringExact(want)),
		}
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkNoPendingInvite,
		Steps: []resource.TestStep{
			{Config: config, ConfigPlanChecks: providertest.ExpectAction(inviteAddr, plancheck.ResourceActionCreate)},
			{
				PreConfig:         func() { srv.setInviteStatus(t, inviteID(1), "accepted") },
				Config:            config,
				ConfigPlanChecks:  providertest.ExpectAction(inviteAddr, plancheck.ResourceActionNoop),
				ConfigStateChecks: status(1, "accepted"),
			},
			{
				PreConfig:         func() { srv.invites.Delete(inviteID(1)) },
				Config:            config,
				ConfigPlanChecks:  providertest.ExpectAction(inviteAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectInvite(2, "jane@example.com", "developer"),
			},
			{
				PreConfig:         func() { srv.setInviteStatus(t, inviteID(2), "expired") },
				Config:            config,
				ConfigPlanChecks:  providertest.ExpectAction(inviteAddr, plancheck.ResourceActionNoop),
				ConfigStateChecks: status(2, "expired"),
			},
		},
	})
	// No invite was pending at destroy, so none was cancelled.
	srv.AssertCalled(routeDeleteInvite, 0)
	srv.AssertCalled(routeCreateInvite, 2)
}

// TestInviteResource_deleteNotFound: an invite already gone (404) when
// Terraform cancels it is not an error, and the 404 is not retried.
func TestInviteResource_deleteNotFound(t *testing.T) {
	t.Parallel()
	srv := newOrgFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{Config: inviteConfig("jane@example.com", "developer"), ConfigPlanChecks: providertest.ExpectAction(inviteAddr, plancheck.ResourceActionCreate)},
			{
				PreConfig:        func() { srv.FailNext(routeDeleteInvite, http.StatusNotFound, 1) },
				Config:           providertest.ProviderConfig(sdktest.AdminKey),
				ConfigPlanChecks: providertest.ExpectAction(inviteAddr, plancheck.ResourceActionDestroy),
			},
		},
	})
	srv.AssertCalled(routeDeleteInvite, 1)
}

// TestInviteResource_apiErrors: an API error on create, refresh or delete is
// an error diagnostic naming the operation.
func TestInviteResource_apiErrors(t *testing.T) {
	t.Parallel()
	srv := newOrgFake(t, 0)
	config := inviteConfig("jane@example.com", "developer")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkNoPendingInvite,
		Steps: []resource.TestStep{
			{
				// The API refuses an invite when no seat is free (400).
				PreConfig:   func() { srv.FailNext(routeCreateInvite, http.StatusBadRequest, 1) },
				Config:      config,
				ExpectError: regexp.MustCompile(`Unable\s+to\s+create\s+invite:\s+API\s+error\s+\(400\s+invalid_request_error\)`),
			},
			{Config: config, ConfigPlanChecks: providertest.ExpectAction(inviteAddr, plancheck.ResourceActionCreate)},
			{
				PreConfig:   func() { srv.FailNext(routeGetInvite, http.StatusForbidden, 1) },
				Config:      config,
				ExpectError: regexp.MustCompile(`Unable\s+to\s+read\s+invite:\s+API\s+error\s+\(403\s+permission_error\)`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeDeleteInvite, http.StatusForbidden, 1) },
				Config:      providertest.ProviderConfig(sdktest.AdminKey),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+delete\s+invite:\s+API\s+error\s+\(403\s+permission_error\)`),
			},
		},
	})
	// The failed POST is not replayed: a 400 is final, and a POST is only
	// retried on 429.
	srv.AssertCalled(routeCreateInvite, 2)
	// The failed delete, then the one of the final destroy.
	srv.AssertCalled(routeDeleteInvite, 2)
}
