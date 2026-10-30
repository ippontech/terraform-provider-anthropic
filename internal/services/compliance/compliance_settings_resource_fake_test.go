// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package compliance_test

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

const settingsAddr = "anthropic_compliance_settings.test"

func settingsConfig(state string) string {
	return providertest.ProviderConfig(sdktest.AdminKey) + fmt.Sprintf("resource \"anthropic_compliance_settings\" \"test\" {\n  state = %q\n}\n", state)
}

func expectSettings(state string) []statecheck.StateCheck {
	return []statecheck.StateCheck{
		statecheck.ExpectKnownValue(settingsAddr, tfjsonpath.New("id"), knownvalue.StringExact(orgID)),
		statecheck.ExpectKnownValue(settingsAddr, tfjsonpath.New("state"), knownvalue.StringExact(state)),
	}
}

// checkState is the CheckDestroy of the settings tests: destroying only
// forgets the singleton, the setting stays as it was.
func (f *complianceFake) checkState(want string) func(*terraform.State) error {
	return func(*terraform.State) error {
		if got := f.currentState(); got != want {
			return fmt.Errorf("compliance settings %q after destroy, want %q (left unchanged)", got, want)
		}
		return nil
	}
}

// TestComplianceSettingsResource_lifecycle enables the Compliance API (the
// ID is the admin key's organization), imports it, disables it in place,
// follows an Access Transparency re-enable as drift, and leaves the setting
// alone on destroy.
func TestComplianceSettingsResource_lifecycle(t *testing.T) {
	t.Parallel()
	srv := newComplianceFake(t, "disabled")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkState("disabled"),
		Steps: []resource.TestStep{
			{
				Config:            settingsConfig("enabled"),
				ConfigPlanChecks:  expectAction(plancheck.ResourceActionCreate),
				ConfigStateChecks: expectSettings("enabled"),
			},
			{
				Config:            settingsConfig("enabled"),
				ResourceName:      settingsAddr,
				ImportState:       true,
				ImportStateId:     "any-id",
				ImportStateVerify: true,
			},
			{
				Config:            settingsConfig("disabled"),
				ConfigPlanChecks:  expectAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectSettings("disabled"),
			},
			{
				// Access Transparency provisioning turns it back on.
				PreConfig:         func() { srv.setState("enabled") },
				Config:            settingsConfig("disabled"),
				ConfigPlanChecks:  expectAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectSettings("disabled"),
			},
		},
	})
	srv.AssertJSONBodies(routeUpdateSettings,
		`{"state":{"type":"enabled"}}`, `{"state":{"type":"disabled"}}`, `{"state":{"type":"disabled"}}`)
	srv.assertRequests(t)
}

// TestComplianceSettingsResource_destroyMakesNoRequest: removing the
// resource only forgets it; no request changes the setting.
func TestComplianceSettingsResource_destroyMakesNoRequest(t *testing.T) {
	t.Parallel()
	srv := newComplianceFake(t, "disabled")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkState("enabled"),
		Steps: []resource.TestStep{
			{Config: settingsConfig("enabled"), ConfigPlanChecks: expectAction(plancheck.ResourceActionCreate)},
			{
				Config:           providertest.ProviderConfig(sdktest.AdminKey),
				ConfigPlanChecks: expectAction(plancheck.ResourceActionDestroy),
			},
		},
	})
	srv.AssertCalled(routeUpdateSettings, 1)
}

// TestComplianceSettingsResource_errors: a 404 on refresh is an error, not a
// removal (the singleton always exists); an unreadable organization fails
// the create before any update; update errors name the operation.
func TestComplianceSettingsResource_errors(t *testing.T) {
	t.Parallel()
	srv := newComplianceFake(t, "disabled")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkState("enabled"),
		Steps: []resource.TestStep{
			{
				PreConfig:   func() { srv.FailNext(routeGetOrganization, http.StatusUnauthorized, 1) },
				Config:      settingsConfig("enabled"),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+read\s+the\s+organization\s+of\s+the\s+admin\s+key:\s+API\s+error\s+\(401\s+authentication_error\)`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeUpdateSettings, http.StatusForbidden, 1) },
				Config:      settingsConfig("enabled"),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+update\s+compliance\s+settings:\s+API\s+error\s+\(403\s+permission_error\)`),
			},
			{Config: settingsConfig("enabled"), ConfigPlanChecks: expectAction(plancheck.ResourceActionCreate)},
			{
				PreConfig:   func() { srv.FailNext(routeGetSettings, http.StatusNotFound, 1) },
				Config:      settingsConfig("enabled"),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+read\s+compliance\s+settings:\s+API\s+error\s+\(404\s+not_found_error\)`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeUpdateSettings, http.StatusBadRequest, 1) },
				Config:      settingsConfig("disabled"),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+update\s+compliance\s+settings:\s+API\s+error\s+\(400\s+invalid_request_error\)`),
			},
		},
	})
	// The organization is read once per create attempt that got past it.
	srv.AssertCalled(routeUpdateSettings, 3)
}

// TestComplianceSettingsResource_organizationWithoutID: an organization
// answer without an ID fails the create and the import.
func TestComplianceSettingsResource_organizationWithoutID(t *testing.T) {
	t.Parallel()
	srv := sdktest.NewServer(t)
	srv.Respond(routeGetOrganization, http.StatusOK, map[string]any{"name": "No ID", "type": "organization"})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv),
		Steps: []resource.TestStep{
			{
				Config:      settingsConfig("enabled"),
				ExpectError: regexp.MustCompile(`The\s+organization\s+response\s+carries\s+no\s+ID`),
			},
			{
				Config:        settingsConfig("enabled"),
				ResourceName:  settingsAddr,
				ImportState:   true,
				ImportStateId: "x",
				ExpectError:   regexp.MustCompile(`The\s+organization\s+response\s+carries\s+no\s+ID`),
			},
		},
	})
	srv.AssertCalled(routeGetOrganization, 2)
}
