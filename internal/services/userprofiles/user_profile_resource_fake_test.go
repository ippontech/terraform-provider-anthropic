// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package userprofiles_test

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

const profileAddr = "anthropic_user_profile.test"

// profileID is the ID the fake gives to the n-th profile it stores.
func profileID(n int) string { return fmt.Sprintf("uprof_%024d", n) }

// at is the n-th tick of a fake collection's clock.
func at(n int) string { return fmt.Sprintf("2026-01-01T00:00:%02dZ", n) }

func profileConfig(extra string) string {
	return providertest.ProviderConfig(sdktest.APIKey) + `
resource "anthropic_user_profile" "test" {
` + extra + `}
`
}

// expectAction checks the action the step plans for the profile and that
// the refresh after the apply plans nothing.
func expectAction(action plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{
		PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(profileAddr, action)},
		PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
	}
}

func stringOrNull(s string) knownvalue.Check {
	if s == "" {
		return knownvalue.Null()
	}
	return knownvalue.StringExact(s)
}

func stringMap(m map[string]string) knownvalue.Check {
	if m == nil {
		return knownvalue.Null()
	}
	checks := map[string]knownvalue.Check{}
	for k, v := range m {
		checks[k] = knownvalue.StringExact(v)
	}
	return knownvalue.MapExact(checks)
}

// grants is the exact check of a trust_grants map of statuses.
func grants(statuses map[string]string) knownvalue.Check {
	checks := map[string]knownvalue.Check{}
	for k, v := range statuses {
		checks[k] = knownvalue.ObjectExact(map[string]knownvalue.Check{"status": knownvalue.StringExact(v)})
	}
	return knownvalue.MapExact(checks)
}

type profile struct {
	id, accessType, externalID, name, createdAt, updatedAt string
	metadata, trustGrants                                  map[string]string
}

func expectProfile(p profile) []statecheck.StateCheck {
	check := func(attr string, c knownvalue.Check) statecheck.StateCheck {
		return statecheck.ExpectKnownValue(profileAddr, tfjsonpath.New(attr), c)
	}
	return []statecheck.StateCheck{
		check("id", knownvalue.StringExact(p.id)),
		check("access_type", knownvalue.StringExact(p.accessType)),
		check("external_id", stringOrNull(p.externalID)),
		check("name", stringOrNull(p.name)),
		check("metadata", stringMap(p.metadata)),
		check("trust_grants", grants(p.trustGrants)),
		check("type", knownvalue.StringExact("user_profile")),
		check("created_at", knownvalue.StringExact(p.createdAt)),
		check("updated_at", knownvalue.StringExact(p.updatedAt)),
	}
}

// checkNoProfile is a CheckDestroy: no profile may be left in the fake.
func (f *profilesFake) checkNoProfile(*terraform.State) error {
	if n := f.profiles.Len(); n != 0 {
		return fmt.Errorf("%d user profiles left after destroy", n)
	}
	return nil
}

// TestUserProfileResource_lifecycle creates a profile, imports it, updates
// it in place (access_type resent, a removed external_id cleared with null,
// a removed metadata key cleared with ""), clears the metadata to {}, picks
// up a trust grant the API added, and deletes it.
func TestUserProfileResource_lifecycle(t *testing.T) {
	t.Parallel()
	srv := newProfilesFake(t, 0)
	full := "  access_type = \"application\"\n  external_id = \"ext-1\"\n  name = \"Acme\"\n  metadata = { tier = \"gold\", region = \"eu\" }\n"
	updated := "  access_type = \"passthrough\"\n  name = \"Acme Corp\"\n  metadata = { tier = \"platinum\" }\n"
	cleared := "  access_type = \"passthrough\"\n  name = \"Acme Corp\"\n  metadata = {}\n"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkNoProfile,
		Steps: []resource.TestStep{
			{
				Config:           profileConfig(full),
				ConfigPlanChecks: expectAction(plancheck.ResourceActionCreate),
				ConfigStateChecks: expectProfile(profile{id: profileID(1), accessType: "application", externalID: "ext-1", name: "Acme",
					metadata: map[string]string{"tier": "gold", "region": "eu"}, createdAt: at(1), updatedAt: at(1)}),
			},
			{Config: profileConfig(full), ResourceName: profileAddr, ImportState: true, ImportStateVerify: true},
			{
				Config:           profileConfig(updated),
				ConfigPlanChecks: expectAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectProfile(profile{id: profileID(1), accessType: "passthrough", name: "Acme Corp",
					metadata: map[string]string{"tier": "platinum"}, createdAt: at(1), updatedAt: at(2)}),
			},
			{
				Config:           profileConfig(cleared),
				ConfigPlanChecks: expectAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectProfile(profile{id: profileID(1), accessType: "passthrough", name: "Acme Corp",
					metadata: map[string]string{}, createdAt: at(1), updatedAt: at(3)}),
			},
			{
				PreConfig: func() {
					p, _ := srv.profiles.Get(profileID(1))
					p["trust_grants"] = map[string]any{"zero_data_retention": map[string]any{"status": "pending"}}
					srv.profiles.Put(p)
				},
				Config:           profileConfig(cleared),
				ConfigPlanChecks: expectAction(plancheck.ResourceActionNoop),
				ConfigStateChecks: expectProfile(profile{id: profileID(1), accessType: "passthrough", name: "Acme Corp",
					metadata: map[string]string{}, trustGrants: map[string]string{"zero_data_retention": "pending"}, createdAt: at(1), updatedAt: at(3)}),
			},
		},
	})

	srv.AssertJSONBodies(routeCreateProfile,
		`{"access_type": "application", "external_id": "ext-1", "name": "Acme", "metadata": {"tier": "gold", "region": "eu"}}`)
	srv.AssertJSONBodies(routeUpdateProfile,
		`{"access_type": "passthrough", "external_id": null, "name": "Acme Corp", "metadata": {"tier": "platinum", "region": ""}}`,
		`{"access_type": "passthrough", "metadata": {"tier": ""}}`)
	srv.AssertCalled(routeDeleteProfile, 1)
	srv.assertRequestShape(t)
}

// TestUserProfileResource_removedOutsideTerraform: a profile the API no
// longer knows leaves the state with a warning and is created again; a 404
// on the delete is success.
func TestUserProfileResource_removedOutsideTerraform(t *testing.T) {
	t.Parallel()
	srv := newProfilesFake(t, 0)
	config := profileConfig("  access_type = \"application\"\n")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{Config: config, ConfigPlanChecks: expectAction(plancheck.ResourceActionCreate)},
			{
				PreConfig:         func() { srv.profiles.Delete(profileID(1)) },
				Config:            config,
				ConfigPlanChecks:  expectAction(plancheck.ResourceActionCreate),
				ConfigStateChecks: expectProfile(profile{id: profileID(2), accessType: "application", createdAt: at(2), updatedAt: at(2)}),
			},
			{
				PreConfig:        func() { srv.FailNext(routeDeleteProfile, http.StatusNotFound, 1) },
				Config:           providertest.ProviderConfig(sdktest.APIKey),
				ConfigPlanChecks: expectAction(plancheck.ResourceActionDestroy),
			},
		},
	})

	srv.AssertJSONBodies(routeCreateProfile, `{"access_type": "application"}`, `{"access_type": "application"}`)
	srv.AssertCalled(routeDeleteProfile, 1)
	srv.assertRequestShape(t)
}

// TestUserProfileResource_apiErrors: an API error on create, refresh,
// update or delete reaches the operator with the provider's summary.
func TestUserProfileResource_apiErrors(t *testing.T) {
	t.Parallel()
	srv := newProfilesFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkNoProfile,
		Steps: []resource.TestStep{
			{
				PreConfig:   func() { srv.FailNext(routeCreateProfile, http.StatusBadRequest, 1) },
				Config:      profileConfig("  access_type = \"application\"\n"),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+create\s+user\s+profile:.*400`),
			},
			{Config: profileConfig("  access_type = \"application\"\n"), ConfigPlanChecks: expectAction(plancheck.ResourceActionCreate)},
			{
				PreConfig:   func() { srv.FailNext(routeGetProfile, http.StatusForbidden, 1) },
				Config:      profileConfig("  access_type = \"application\"\n"),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+read\s+user\s+profile:.*403`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeUpdateProfile, http.StatusBadRequest, 1) },
				Config:      profileConfig("  access_type = \"passthrough\"\n"),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+update\s+user\s+profile:.*400`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeDeleteProfile, http.StatusBadRequest, 1) },
				Config:      providertest.ProviderConfig(sdktest.APIKey),
				ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+delete\s+user\s+profile:.*400`),
			},
		},
	})

	srv.AssertCalled(routeCreateProfile, 2)
	srv.AssertCalled(routeUpdateProfile, 1)
	srv.AssertCalled(routeDeleteProfile, 2)
	srv.assertRequestShape(t)
}
