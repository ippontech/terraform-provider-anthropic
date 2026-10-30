// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package apikeys_test

import (
	"fmt"
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

const keyAddr = "anthropic_api_key.test"

// keyConfig declares the resource with the given arguments ("" leaves one
// out: both are Optional and Computed).
func keyConfig(name, status string) string {
	var args string
	if name != "" {
		args += fmt.Sprintf("  name   = %q\n", name)
	}
	if status != "" {
		args += fmt.Sprintf("  status = %q\n", status)
	}
	return providertest.ProviderConfig(sdktest.AdminKey) + "resource \"anthropic_api_key\" \"test\" {\n" + args + "}\n"
}

// importKey is the first step of the resource tests: the resource is
// import-only, so the key enters the state through terraform import.
func importKey(n int, config string) resource.TestStep {
	return resource.TestStep{
		Config:             config,
		ResourceName:       keyAddr,
		ImportState:        true,
		ImportStateId:      keyID(n),
		ImportStatePersist: true,
		ImportStateCheck: func(states []*terraform.InstanceState) error {
			if len(states) != 1 || states[0].ID != keyID(n) {
				return fmt.Errorf("imported %d instances, want %s", len(states), keyID(n))
			}
			return nil
		},
	}
}

func expectKey(n, shape int, name, status string) []statecheck.StateCheck {
	var checks []statecheck.StateCheck
	for attr, c := range keyChecks(n, shape, name, status) {
		checks = append(checks, statecheck.ExpectKnownValue(keyAddr, tfjsonpath.New(attr), c))
	}
	return checks
}

// TestAPIKeyResource_lifecycle imports a key, renames it and deactivates it
// in place (POST /v1/organizations/api_keys/{id} with the planned name and
// status), verifies a fresh import matches, and deactivates the key on
// destroy instead of deleting it.
func TestAPIKeyResource_lifecycle(t *testing.T) {
	t.Parallel()
	srv := newKeysFake(t, 0)
	srv.keys.Put(keyJSON(1, workspaceUserKey, "CI key", "active"))
	srv.keys.Put(keyJSON(2, workspaceUserKey, "Other key", "active"))

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy: func(*terraform.State) error {
			for id, want := range map[string]string{keyID(1): "inactive", keyID(2): "active"} {
				if k, _ := srv.keys.Get(id); k["status"] != want {
					return fmt.Errorf("key %s status %v after destroy, want %s", id, k["status"], want)
				}
			}
			return nil
		},
		Steps: []resource.TestStep{
			importKey(1, keyConfig("CI key", "active")),
			{
				Config:            keyConfig("CI key", "active"),
				ConfigPlanChecks:  expectKeyAction(plancheck.ResourceActionNoop),
				ConfigStateChecks: expectKey(1, workspaceUserKey, "CI key", "active"),
			},
			{
				Config:            keyConfig("CI key (rotated)", "active"),
				ConfigPlanChecks:  expectKeyAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectKey(1, workspaceUserKey, "CI key (rotated)", "active"),
			},
			{
				Config:            keyConfig("CI key (rotated)", "archived"),
				ConfigPlanChecks:  expectKeyAction(plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectKey(1, workspaceUserKey, "CI key (rotated)", "archived"),
			},
			{
				Config:            keyConfig("CI key (rotated)", "archived"),
				ResourceName:      keyAddr,
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})

	// Two updates, then the deactivation of the destroy, all on key 1.
	srv.AssertJSONBodies(routeUpdateKey,
		`{"name":"CI key (rotated)","status":"active"}`,
		`{"name":"CI key (rotated)","status":"archived"}`,
		`{"status":"inactive"}`)
	for _, r := range srv.Requests(routeUpdateKey) {
		if r.Path != "/v1/organizations/api_keys/"+keyID(1) {
			t.Errorf("update sent to %s, want the managed key", r.Path)
		}
	}
	srv.assertAdminRequests(t)
}

// TestAPIKeyResource_computedArguments: without name and status in the
// configuration the imported values stay (Optional, Computed,
// UseStateForUnknown), the plan is empty, and nullable fields stay null.
func TestAPIKeyResource_computedArguments(t *testing.T) {
	t.Parallel()
	srv := newKeysFake(t, 0)
	srv.keys.Put(keyJSON(3, legacyDefaultKey, "Legacy", "active"))

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			importKey(3, keyConfig("", "")),
			{
				Config:            keyConfig("", ""),
				ConfigPlanChecks:  expectKeyAction(plancheck.ResourceActionNoop),
				ConfigStateChecks: expectKey(3, legacyDefaultKey, "Legacy", "active"),
			},
		},
	})
	srv.AssertJSONBodies(routeUpdateKey, `{"status":"inactive"}`)
}

// TestAPIKeyResource_createRefused: the resource cannot create a key; the
// error points to terraform import, before any request.
func TestAPIKeyResource_createRefused(t *testing.T) {
	t.Parallel()
	srv := newKeysFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config:      keyConfig("New key", ""),
			ExpectError: regexp.MustCompile(`(?s)API\s+Key\s+Creation\s+Not\s+Supported.*terraform\s+import\s+anthropic_api_key`),
		}},
	})
	srv.AssertCalled("", 0)
}

// TestAPIKeyResource_removedOutsideTerraform: a key that no longer exists
// (404) leaves the state on refresh; the next plan wants to create it, which
// the resource refuses.
func TestAPIKeyResource_removedOutsideTerraform(t *testing.T) {
	t.Parallel()
	srv := newKeysFake(t, 0)
	srv.keys.Put(keyJSON(2, orgServiceAccountKey, "Deploy", "active"))

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			importKey(2, keyConfig("Deploy", "active")),
			{
				PreConfig: func() { srv.keys.Delete(keyID(2)) },
				Config:    keyConfig("Deploy", "active"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(keyAddr, plancheck.ResourceActionCreate)},
				},
				ExpectError: regexp.MustCompile(`API\s+Key\s+Creation\s+Not\s+Supported`),
			},
		},
	})
	srv.AssertCalled(routeUpdateKey, 0)
}

// TestAPIKeyResource_deactivateNotFound: a key already gone (404) when
// Terraform deactivates it on destroy is not an error, and the 404 is not
// retried.
func TestAPIKeyResource_deactivateNotFound(t *testing.T) {
	t.Parallel()
	srv := newKeysFake(t, 0)
	srv.keys.Put(keyJSON(1, workspaceUserKey, "CI key", "active"))

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			importKey(1, keyConfig("CI key", "active")),
			{
				PreConfig:        func() { srv.FailNext(routeUpdateKey, http.StatusNotFound, 1) },
				Config:           providertest.ProviderConfig(sdktest.AdminKey),
				ConfigPlanChecks: expectKeyAction(plancheck.ResourceActionDestroy),
			},
		},
	})
	srv.AssertCalled(routeUpdateKey, 1)
}

// TestAPIKeyResource_apiErrors: an API error on update, refresh or
// deactivation is an error diagnostic naming the operation.
func TestAPIKeyResource_apiErrors(t *testing.T) {
	t.Parallel()
	srv := newKeysFake(t, 0)
	srv.keys.Put(keyJSON(1, workspaceUserKey, "CI key", "active"))

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy: func(*terraform.State) error {
			if k, _ := srv.keys.Get(keyID(1)); k["status"] != "inactive" {
				return fmt.Errorf("key status %v after destroy, want inactive", k["status"])
			}
			return nil
		},
		Steps: []resource.TestStep{
			importKey(1, keyConfig("CI key", "active")),
			{
				PreConfig:   func() { srv.FailNext(routeUpdateKey, http.StatusBadRequest, 1) },
				Config:      keyConfig("Renamed", "active"),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+update\s+API\s+key:\s+API\s+error\s+\(400\s+invalid_request_error\)`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeGetKey, http.StatusForbidden, 1) },
				Config:      keyConfig("CI key", "active"),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+read\s+API\s+key:\s+API\s+error\s+\(403\s+permission_error\)`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeUpdateKey, http.StatusForbidden, 1) },
				Config:      providertest.ProviderConfig(sdktest.AdminKey),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+deactivate\s+API\s+key:\s+API\s+error\s+\(403\s+permission_error\)`),
			},
		},
	})
	// The failed update, the failed deactivation, then the final destroy's;
	// none is replayed.
	srv.AssertJSONBodies(routeUpdateKey, `{"name":"Renamed","status":"active"}`, `{"status":"inactive"}`, `{"status":"inactive"}`)
}
