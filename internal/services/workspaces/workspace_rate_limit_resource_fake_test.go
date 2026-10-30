// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces_test

import (
	"fmt"
	"maps"
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

const limitAddr = "anthropic_workspace_rate_limit.test"

// sonnetLimits and batchLimits are the organization's rate-limit entries of
// the rate-limit fakes: a model group and an API surface.
var (
	sonnetLimits = orgLimit{
		id: rateLimitID(1), groupType: "model_group", displayName: "Claude Sonnet 4.x",
		models: []string{"claude-sonnet-4-5", "claude-sonnet-4-5-20250929"},
		limits: map[string]int64{"requests_per_minute": 4000, "input_tokens_per_minute": 2000000, "output_tokens_per_minute": 400000},
	}
	batchLimits = orgLimit{
		id: rateLimitID(2), groupType: "batch",
		limits: map[string]int64{"requests_per_minute": 1000},
	}
)

// newLimitsFake is a fake with one workspace and the two organization
// entries.
func newLimitsFake(t *testing.T, pageSize int) *wsFake {
	t.Helper()
	srv := newWSFake(t, pageSize)
	srv.putWorkspace(workspaceJSON(1, "Limited"))
	srv.putOrgLimit(sonnetLimits)
	srv.putOrgLimit(batchLimits)
	return srv
}

func limitConfig(rlID string, limits map[string]int64) string {
	var body string
	for k, v := range limits {
		body += fmt.Sprintf("    %s = %d\n", k, v)
	}
	return providertest.ProviderConfig(sdktest.AdminKey) + fmt.Sprintf(`
resource "anthropic_workspace_rate_limit" "test" {
  workspace_id  = %q
  rate_limit_id = %q
  limits = {
%s  }
}
`, workspaceID(1), rlID, body)
}

func expectLimits(l orgLimit, limits map[string]int64) []statecheck.StateCheck {
	values := map[string]knownvalue.Check{}
	for k, v := range limits {
		values[k] = knownvalue.Int64Exact(v)
	}
	group := map[string]knownvalue.Check{
		"id": knownvalue.StringExact(groupID(l.groupType)), "type": knownvalue.StringExact(l.groupType), "display_name": knownvalue.Null(),
	}
	if l.displayName != "" {
		group["display_name"] = knownvalue.StringExact(l.displayName)
	}
	return []statecheck.StateCheck{
		statecheck.ExpectKnownValue(limitAddr, tfjsonpath.New("id"), knownvalue.StringExact(workspaceID(1)+":"+l.id)),
		statecheck.ExpectKnownValue(limitAddr, tfjsonpath.New("workspace_id"), knownvalue.StringExact(workspaceID(1))),
		statecheck.ExpectKnownValue(limitAddr, tfjsonpath.New("rate_limit_id"), knownvalue.StringExact(l.id)),
		statecheck.ExpectKnownValue(limitAddr, tfjsonpath.New("limits"), knownvalue.MapExact(values)),
		statecheck.ExpectKnownValue(limitAddr, tfjsonpath.New("group"), knownvalue.ObjectExact(group)),
	}
}

// checkOverrides fails unless the workspace's overrides of the entry are
// exactly want.
func (f *wsFake) checkOverrides(rlID string, want map[string]int64) func(*terraform.State) error {
	return func(*terraform.State) error {
		if got := f.overridesOf(workspaceID(1), rlID); !maps.Equal(got, want) {
			return fmt.Errorf("overrides of %s = %v, want %v", rlID, got, want)
		}
		return nil
	}
}

// TestWorkspaceRateLimitResource_lifecycle sets two overrides, imports them,
// changes one and swaps another in place (the removed type is sent as null),
// and clears the managed types on destroy.
func TestWorkspaceRateLimitResource_lifecycle(t *testing.T) {
	t.Parallel()
	srv := newLimitsFake(t, 0)
	first := map[string]int64{"requests_per_minute": 100, "output_tokens_per_minute": 5000}
	second := map[string]int64{"requests_per_minute": 200, "tokens_per_day": 1000000}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkOverrides(sonnetLimits.id, map[string]int64{}),
		Steps: []resource.TestStep{
			{
				Config:            limitConfig(sonnetLimits.id, first),
				ConfigPlanChecks:  providertest.ExpectAction(limitAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectLimits(sonnetLimits, first),
			},
			{
				Config:            limitConfig(sonnetLimits.id, first),
				ResourceName:      limitAddr,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config:            limitConfig(sonnetLimits.id, second),
				ConfigPlanChecks:  providertest.ExpectAction(limitAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectLimits(sonnetLimits, second),
			},
		},
	})

	srv.AssertJSONBodies(routeSetWSLimits,
		`{"limits":[{"type":"output_tokens_per_minute","value":5000},{"type":"requests_per_minute","value":100}]}`,
		`{"limits":[{"type":"output_tokens_per_minute","value":null},{"type":"requests_per_minute","value":200},{"type":"tokens_per_day","value":1000000}]}`,
		`{"limits":[{"type":"requests_per_minute","value":null},{"type":"tokens_per_day","value":null}]}`)
	// Every refresh lists the stored overrides only.
	srv.AssertWalks(routeListWSLimits, "limit=1000")
	srv.assertAdminRequests(t)
}

// TestWorkspaceRateLimitResource_unmanagedOverridesKept: overrides of types
// the resource does not manage stay out of state and survive the destroy;
// a managed type cleared out of band shows up as drift and is set again.
func TestWorkspaceRateLimitResource_unmanagedOverridesKept(t *testing.T) {
	t.Parallel()
	srv := newLimitsFake(t, 0)
	srv.setOverride(workspaceID(1), batchLimits.id, "input_tokens_per_minute", 777)
	managed := map[string]int64{"requests_per_minute": 50}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkOverrides(batchLimits.id, map[string]int64{"input_tokens_per_minute": 777}),
		Steps: []resource.TestStep{
			{
				Config:            limitConfig(batchLimits.id, managed),
				ConfigPlanChecks:  providertest.ExpectAction(limitAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expectLimits(batchLimits, managed),
			},
			{
				PreConfig:         func() { srv.clearOverride(workspaceID(1), batchLimits.id, "requests_per_minute") },
				Config:            limitConfig(batchLimits.id, managed),
				ConfigPlanChecks:  providertest.ExpectAction(limitAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expectLimits(batchLimits, managed),
			},
		},
	})
	srv.AssertJSONBodies(routeSetWSLimits,
		`{"limits":[{"type":"requests_per_minute","value":50}]}`,
		`{"limits":[{"type":"requests_per_minute","value":50}]}`,
		`{"limits":[{"type":"requests_per_minute","value":null}]}`)
}

// TestWorkspaceRateLimitResource_workspaceGone: when the workspace no longer
// exists (404 on the list) the resource leaves the state, and the next plan
// sets the overrides again, which the API refuses for a missing workspace.
func TestWorkspaceRateLimitResource_workspaceGone(t *testing.T) {
	t.Parallel()
	srv := newLimitsFake(t, 0)
	config := limitConfig(sonnetLimits.id, map[string]int64{"requests_per_minute": 10})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{Config: config, ConfigPlanChecks: providertest.ExpectAction(limitAddr, plancheck.ResourceActionCreate)},
			{
				PreConfig: func() { srv.deleteWS(workspaceID(1)) },
				Config:    config,
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{plancheck.ExpectResourceAction(limitAddr, plancheck.ResourceActionCreate)},
				},
				ExpectError: regexp.MustCompile(`Unable\s+to\s+set\s+workspace\s+rate-limit\s+overrides:\s+API\s+error\s+\(404\s+not_found_error\)`),
			},
		},
	})
	srv.AssertCalled(routeSetWSLimits, 2)
}

// TestWorkspaceRateLimitResource_importEveryOverride: an import takes every
// override of the group; a group without override cannot be imported, nor a
// malformed ID.
func TestWorkspaceRateLimitResource_importEveryOverride(t *testing.T) {
	t.Parallel()
	srv := newLimitsFake(t, 0)
	srv.setOverride(workspaceID(1), sonnetLimits.id, "requests_per_minute", 11)
	srv.setOverride(workspaceID(1), sonnetLimits.id, "tool_uses_per_second", 3)
	imported := map[string]int64{"requests_per_minute": 11, "tool_uses_per_second": 3}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkOverrides(sonnetLimits.id, map[string]int64{}),
		Steps: []resource.TestStep{
			{
				Config:        limitConfig(batchLimits.id, map[string]int64{"requests_per_minute": 1}),
				ResourceName:  limitAddr,
				ImportState:   true,
				ImportStateId: workspaceID(1) + ":" + batchLimits.id,
				ExpectError:   regexp.MustCompile(`Cannot\s+import\s+non-existent\s+remote\s+object`),
			},
			{
				Config:        limitConfig(batchLimits.id, map[string]int64{"requests_per_minute": 1}),
				ResourceName:  limitAddr,
				ImportState:   true,
				ImportStateId: workspaceID(1),
				ExpectError:   regexp.MustCompile(`(?s)Invalid\s+import\s+ID.*<workspace_id>:<rate_limit_id>`),
			},
			{
				Config:             limitConfig(sonnetLimits.id, imported),
				ResourceName:       limitAddr,
				ImportState:        true,
				ImportStateId:      workspaceID(1) + ":" + sonnetLimits.id,
				ImportStatePersist: true,
			},
			{
				Config:            limitConfig(sonnetLimits.id, imported),
				ConfigPlanChecks:  providertest.ExpectAction(limitAddr, plancheck.ResourceActionNoop),
				ConfigStateChecks: expectLimits(sonnetLimits, imported),
			},
		},
	})
	srv.AssertJSONBodies(routeSetWSLimits, `{"limits":[{"type":"requests_per_minute","value":null},{"type":"tool_uses_per_second","value":null}]}`)
}

// TestWorkspaceRateLimitResource_clearNotFound: a 404 when the destroy
// clears the overrides is not an error.
func TestWorkspaceRateLimitResource_clearNotFound(t *testing.T) {
	t.Parallel()
	srv := newLimitsFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{
			{Config: limitConfig(batchLimits.id, map[string]int64{"requests_per_minute": 5}), ConfigPlanChecks: providertest.ExpectAction(limitAddr, plancheck.ResourceActionCreate)},
			{
				PreConfig:        func() { srv.FailNext(routeSetWSLimits, http.StatusNotFound, 1) },
				Config:           providertest.ProviderConfig(sdktest.AdminKey),
				ConfigPlanChecks: providertest.ExpectAction(limitAddr, plancheck.ResourceActionDestroy),
			},
		},
	})
	srv.AssertCalled(routeSetWSLimits, 2)
}

// TestWorkspaceRateLimitResource_apiErrors: an API error on create, refresh,
// update or clear is an error diagnostic naming the operation.
func TestWorkspaceRateLimitResource_apiErrors(t *testing.T) {
	t.Parallel()
	srv := newLimitsFake(t, 0)
	config := func(v int64) string {
		return limitConfig(batchLimits.id, map[string]int64{"requests_per_minute": v})
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		CheckDestroy:             srv.checkOverrides(batchLimits.id, map[string]int64{}),
		Steps: []resource.TestStep{
			{
				PreConfig:   func() { srv.FailNext(routeSetWSLimits, http.StatusBadRequest, 1) },
				Config:      config(1),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+set\s+workspace\s+rate-limit\s+overrides:\s+API\s+error\s+\(400\s+invalid_request_error\)`),
			},
			{Config: config(1), ConfigPlanChecks: providertest.ExpectAction(limitAddr, plancheck.ResourceActionCreate)},
			{
				PreConfig:   func() { srv.FailNext(routeListWSLimits, http.StatusForbidden, 1) },
				Config:      config(1),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+list\s+workspace\s+rate\s+limits:\s+API\s+error\s+\(403\s+permission_error\)`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeSetWSLimits, http.StatusBadRequest, 1) },
				Config:      config(2),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+update\s+workspace\s+rate-limit\s+overrides:\s+API\s+error\s+\(400\s+invalid_request_error\)`),
			},
			{
				PreConfig:   func() { srv.FailNext(routeSetWSLimits, http.StatusForbidden, 1) },
				Config:      providertest.ProviderConfig(sdktest.AdminKey),
				ExpectError: regexp.MustCompile(`Unable\s+to\s+clear\s+workspace\s+rate-limit\s+overrides:\s+API\s+error\s+\(403\s+permission_error\)`),
			},
		},
	})
	// Two creates, one update, two clears: none replayed.
	srv.AssertCalled(routeSetWSLimits, 5)
}

// TestWorkspaceRateLimitResource_invalidLimits: an unknown limiter type, a
// negative value or an empty map fail the plan, before any request.
func TestWorkspaceRateLimitResource_invalidLimits(t *testing.T) {
	t.Parallel()
	for name, limits := range map[string]map[string]int64{
		"unknown type":   {"requests_per_hour": 1},
		"negative value": {"requests_per_minute": -1},
		"empty":          {},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newLimitsFake(t, 0)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
				Steps: []resource.TestStep{{
					Config:      limitConfig(sonnetLimits.id, limits),
					ExpectError: regexp.MustCompile(`Invalid\s+Attribute\s+Value`),
				}},
			})
			srv.AssertCalled("", 0)
		})
	}
}
