// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation_test

import (
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
)

const ruleWorkspaceAddr = "anthropic_federation_rule_workspace.test"

// seedRule stores a rule outside Terraform, for the workspace enablements.
func seedRule(f *federationFake, id string) {
	f.rules.Put(map[string]any{"type": "federation_rule", "id": id, "name": "rule " + id, "archived_at": nil})
}

func ruleWorkspaceConfig(workspace string) string {
	return config(`
resource "anthropic_federation_rule_workspace" "test" {
  federation_rule_id = "fdrl_seeded"
  workspace_id       = "` + workspace + `"
}
`)
}

// TestFederationRuleWorkspaceResource_lifecycle enables a rule for a
// workspace (workspace_name, null in the add response, is looked up in the
// list), imports it by "<rule>:<workspace>", replaces it for another
// workspace, and removes it on destroy.
func TestFederationRuleWorkspaceResource_lifecycle(t *testing.T) {
	t.Parallel()
	srv := newFederationFake(t, 0)
	seedRule(srv, "fdrl_seeded")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(srv),
		Steps: []resource.TestStep{
			{
				Config:           ruleWorkspaceConfig("wrkspc_a"),
				ConfigPlanChecks: expectAction(ruleWorkspaceAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expect(ruleWorkspaceAddr, map[string]knownvalue.Check{
					"id": knownvalue.StringExact("fdrl_seeded:wrkspc_a"), "federation_rule_id": knownvalue.StringExact("fdrl_seeded"),
					"workspace_id": knownvalue.StringExact("wrkspc_a"), "workspace_name": knownvalue.StringExact("alpha"),
					"created_at": knownvalue.StringExact(at(1)), "created_by_actor_id": knownvalue.StringExact("user_001"),
				}),
			},
			{
				Config:            ruleWorkspaceConfig("wrkspc_a"),
				ResourceName:      ruleWorkspaceAddr,
				ImportState:       true,
				ImportStateId:     "fdrl_seeded:wrkspc_a",
				ImportStateVerify: true,
			},
			{
				Config:            ruleWorkspaceConfig("wrkspc_b"),
				ConfigPlanChecks:  expectAction(ruleWorkspaceAddr, plancheck.ResourceActionReplace),
				ConfigStateChecks: expect(ruleWorkspaceAddr, map[string]knownvalue.Check{"workspace_name": knownvalue.StringExact("beta")}),
			},
		},
	})

	srv.AssertJSONBodies(routeAddWorkspace, `{"workspace_id": "wrkspc_a"}`, `{"workspace_id": "wrkspc_b"}`)
	removed := srv.Requests(routeRemoveWorkspace)
	if len(removed) != 2 || removed[0].Path != "/v1/organizations/federation_rules/fdrl_seeded/workspaces/wrkspc_a" ||
		removed[1].Path != "/v1/organizations/federation_rules/fdrl_seeded/workspaces/wrkspc_b" {
		t.Errorf("remove requests = %+v, want wrkspc_a then wrkspc_b", removed)
	}
	if n := srv.workspaces.Len(); n != 0 {
		t.Errorf("%d enablements left after destroy", n)
	}
	srv.assertRequestShape()
}

// TestFederationRuleWorkspaceResource_goneOutOfBand: an enablement removed
// behind Terraform's back is dropped by the refresh (after the consistency
// window) and created again; so is one whose rule is gone (404 on the list);
// destroy succeeds when the remove answers 404.
func TestFederationRuleWorkspaceResource_goneOutOfBand(t *testing.T) {
	t.Parallel()
	srv := newFederationFake(t, 0)
	seedRule(srv, "fdrl_seeded")
	cfg := ruleWorkspaceConfig("wrkspc_a")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(srv),
		Steps: []resource.TestStep{
			{Config: cfg},
			{
				PreConfig:        func() { srv.workspaces.Delete("fdrl_seeded:wrkspc_a") },
				Config:           cfg,
				ConfigPlanChecks: expectAction(ruleWorkspaceAddr, plancheck.ResourceActionCreate),
			},
			{
				PreConfig: func() {
					srv.rules.Delete("fdrl_seeded")
					srv.workspaces.Delete("fdrl_seeded:wrkspc_a")
				},
				Config:             cfg,
				PlanOnly:           true,
				ExpectNonEmptyPlan: true,
			},
			{
				PreConfig: func() {
					seedRule(srv, "fdrl_seeded")
					srv.workspaces.Put(map[string]any{
						"id": "fdrl_seeded:wrkspc_a", "type": "federation_rule_workspace", "federation_rule_id": "fdrl_seeded",
						"workspace_id": "wrkspc_a", "workspace_name": "alpha", "created_at": at(9), "created_by_actor_id": "user_001",
					})
					srv.FailNext(routeRemoveWorkspace, http.StatusNotFound, 1)
				},
				Config:  cfg,
				Destroy: true,
			},
		},
	})
	srv.AssertCalled(routeAddWorkspace, 2)
	if reqs := srv.Requests(routeRemoveWorkspace); len(reqs) != 1 || reqs[0].Fault != "status 404" {
		t.Errorf("remove requests = %+v, want one answered 404", reqs)
	}
}

// TestFederationRuleWorkspaceResource_errors: an API error on add, refresh
// or remove is an error diagnostic; a malformed import ID is rejected.
func TestFederationRuleWorkspaceResource_errors(t *testing.T) {
	t.Parallel()
	cfg := ruleWorkspaceConfig("wrkspc_a")
	for name, tc := range map[string]struct {
		route string
		steps func(fail func()) []resource.TestStep
	}{
		"add": {routeAddWorkspace, func(fail func()) []resource.TestStep {
			return []resource.TestStep{{PreConfig: fail, Config: cfg, ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+enable\s+federation\s+rule\s+for\s+workspace:.*403\s+Forbidden`)}}
		}},
		"refresh": {routeListWorkspaces, func(fail func()) []resource.TestStep {
			return []resource.TestStep{
				{Config: cfg},
				{PreConfig: fail, RefreshState: true, ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+list\s+federation\s+rule\s+workspaces:.*403\s+Forbidden`)},
			}
		}},
		"remove": {routeRemoveWorkspace, func(fail func()) []resource.TestStep {
			return []resource.TestStep{
				{Config: cfg},
				{PreConfig: fail, Config: cfg, Destroy: true, ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+disable\s+federation\s+rule\s+for\s+workspace:.*403\s+Forbidden`)},
			}
		}},
		"import ID": {routeListWorkspaces, func(func()) []resource.TestStep {
			return []resource.TestStep{{
				Config: cfg, ResourceName: ruleWorkspaceAddr, ImportState: true, ImportStateId: "fdrl_seeded",
				ExpectError: regexp.MustCompile(`(?s)Invalid\s+import\s+ID.*<federation_rule_id>:<workspace_id>`),
			}}
		}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newFederationFake(t, 0)
			seedRule(srv, "fdrl_seeded")
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(srv),
				Steps:                    tc.steps(func() { srv.FailNext(tc.route, http.StatusForbidden, 1) }),
			})
			faults := 0
			for _, r := range srv.Requests(tc.route) {
				if r.Fault != "" {
					faults++
				}
			}
			want := 1
			if name == "import ID" {
				want = 0
			}
			if faults != want {
				t.Errorf("%s: %d failed requests, want %d", tc.route, faults, want)
			}
		})
	}
}
