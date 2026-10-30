// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces_test

import (
	"maps"
	"regexp"
	"slices"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

func groupCheck(l orgLimit) knownvalue.Check {
	var display knownvalue.Check = knownvalue.Null()
	if l.displayName != "" {
		display = knownvalue.StringExact(l.displayName)
	}
	return knownvalue.ObjectExact(map[string]knownvalue.Check{
		"id": knownvalue.StringExact(groupID(l.groupType)), "type": knownvalue.StringExact(l.groupType), "display_name": display,
	})
}

func modelsCheck(l orgLimit) knownvalue.Check {
	if l.groupType != "model_group" {
		return knownvalue.Null()
	}
	var models []knownvalue.Check
	for _, m := range l.models {
		models = append(models, knownvalue.StringExact(m))
	}
	return knownvalue.ListExact(models)
}

// wsEntryCheck is the exact data source item for l in workspace 1 with the
// overrides over; inherited lists the organization's other values too.
func wsEntryCheck(l orgLimit, over map[string]int64, inherited bool) knownvalue.Check {
	types := map[string]int64{}
	maps.Copy(types, over)
	if inherited {
		maps.Copy(types, l.limits)
		maps.Copy(types, over)
	}
	var limits []knownvalue.Check
	for _, t := range slices.Sorted(maps.Keys(types)) {
		var orgLimit knownvalue.Check = knownvalue.Null()
		if v, ok := l.limits[t]; ok {
			orgLimit = knownvalue.Int64Exact(v)
		}
		value, source := l.limits[t], "organization"
		if v, ok := over[t]; ok {
			value, source = v, "workspace"
		}
		limits = append(limits, knownvalue.ObjectExact(map[string]knownvalue.Check{
			"type": knownvalue.StringExact(t), "value": knownvalue.Int64Exact(value), "org_limit": orgLimit,
			"source": knownvalue.ObjectExact(map[string]knownvalue.Check{"type": knownvalue.StringExact(source)}),
		}))
	}
	return knownvalue.ObjectExact(map[string]knownvalue.Check{
		"type": knownvalue.StringExact("workspace_rate_limit"), "rate_limit_id": knownvalue.StringExact(l.id),
		"group": groupCheck(l), "group_type": knownvalue.StringExact(l.groupType), "models": modelsCheck(l),
		"limits": knownvalue.ListExact(limits),
	})
}

const wsLimitsAddr = "data.anthropic_workspace_rate_limits.test"

func wsLimitsConfig(extra string) string {
	return providertest.ProviderConfig(sdktest.AdminKey) + "data \"anthropic_workspace_rate_limits\" \"test\" {\n  workspace_id = \"" + workspaceID(1) + "\"\n" + extra + "}\n"
}

// TestWorkspaceRateLimitsDataSource_pages lists the workspace's overrides,
// one entry per page, through the real Read: it follows next_page with page
// until next_page is null. A group without override is not listed.
func TestWorkspaceRateLimitsDataSource_pages(t *testing.T) {
	t.Parallel()
	srv := newLimitsFake(t, 1)
	tokens := orgLimit{id: rateLimitID(3), groupType: "token_count", limits: map[string]int64{"requests_per_minute": 2000}}
	srv.putOrgLimit(tokens)
	srv.setOverride(workspaceID(1), sonnetLimits.id, "requests_per_minute", 50)
	srv.setOverride(workspaceID(1), sonnetLimits.id, "fast_otpm", 9) // no organization value: org_limit null
	srv.setOverride(workspaceID(1), tokens.id, "requests_per_minute", 7)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: wsLimitsConfig(""),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(wsLimitsAddr, tfjsonpath.New("rate_limits"), knownvalue.ListExact([]knownvalue.Check{
					wsEntryCheck(sonnetLimits, map[string]int64{"requests_per_minute": 50, "fast_otpm": 9}, false),
					wsEntryCheck(tokens, map[string]int64{"requests_per_minute": 7}, false),
				})),
			},
		}},
	})
	srv.AssertWalks(routeListWSLimits, "limit=1000", "limit=1000&page="+sdktest.NextPageToken(sonnetLimits.id))
	srv.AssertAllPagesRead(routeListWSLimits)
	srv.assertAdminRequests(t)
}

// TestWorkspaceRateLimitsDataSource_filters sends group_type and
// include_inherited on every page; inherited values come with source
// organization.
func TestWorkspaceRateLimitsDataSource_filters(t *testing.T) {
	t.Parallel()
	srv := newLimitsFake(t, 1)
	haiku := orgLimit{
		id: rateLimitID(3), groupType: "model_group", displayName: "Claude Haiku 4.x",
		models: []string{"claude-haiku-4-5"}, limits: map[string]int64{"requests_per_minute": 9000},
	}
	srv.putOrgLimit(haiku)
	srv.setOverride(workspaceID(1), sonnetLimits.id, "requests_per_minute", 50)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: wsLimitsConfig("  group_type = \"model_group\"\n  include_inherited = true\n"),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(wsLimitsAddr, tfjsonpath.New("rate_limits"), knownvalue.ListExact([]knownvalue.Check{
					wsEntryCheck(sonnetLimits, map[string]int64{"requests_per_minute": 50}, true),
					wsEntryCheck(haiku, nil, true),
				})),
			},
		}},
	})
	const filters = "group_type=model_group&include_inherited=true&limit=1000"
	srv.AssertWalks(routeListWSLimits, filters, filters+"&page="+sdktest.NextPageToken(sonnetLimits.id))
	srv.AssertAllPagesRead(routeListWSLimits)
}

// TestWorkspaceRateLimitsDataSource_empty: no override is an empty list.
func TestWorkspaceRateLimitsDataSource_empty(t *testing.T) {
	t.Parallel()
	srv := newLimitsFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config: wsLimitsConfig("  include_inherited = false\n"),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(wsLimitsAddr, tfjsonpath.New("rate_limits"), knownvalue.ListExact([]knownvalue.Check{})),
			},
		}},
	})
	srv.AssertWalks(routeListWSLimits, "limit=1000")
}

// TestWorkspaceRateLimitsDataSource_unknownWorkspace: the 404 of an unknown
// workspace is an error.
func TestWorkspaceRateLimitsDataSource_unknownWorkspace(t *testing.T) {
	t.Parallel()
	srv := newWSFake(t, 0)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []resource.TestStep{{
			Config:      wsLimitsConfig(""),
			ExpectError: regexp.MustCompile(`Unable\s+to\s+list\s+workspace\s+rate\s+limits:\s+API\s+error\s+\(404\s+not_found_error\)`),
		}},
	})
	srv.AssertCalled(routeListWSLimits, 1)
}
