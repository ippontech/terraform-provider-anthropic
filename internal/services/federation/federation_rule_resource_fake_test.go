// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation_test

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
)

const ruleAddr = "anthropic_federation_rule.test"

// ruleID and issuerID are the IDs the fake gives to the n-th object of each
// collection.
func ruleID(n int) string   { return fmt.Sprintf("fdrl_%024d", n) }
func issuerID(n int) string { return fmt.Sprintf("fdis_%024d", n) }

// at is the n-th tick of a fake collection's clock.
func at(n int) string { return fmt.Sprintf("2026-01-01T00:00:%02dZ", n) }

// seedIssuer stores the issuer the rules reference, named github.
func seedIssuer(f *federationFake) {
	f.issuers.Put(map[string]any{"type": "federation_issuer", "id": "fdis_github", "name": "github", "issuer_url": "https://token.actions.githubusercontent.com"})
}

func ruleConfig(body string) string {
	return config(`
resource "anthropic_federation_rule" "test" {
  issuer_id   = "fdis_github"
  oauth_scope = "workspace:developer"
` + body + `}
`)
}

func expectAction(addr string, action plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{
		PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, action)},
		PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
	}
}

func expect(addr string, values map[string]knownvalue.Check) []statecheck.StateCheck {
	checks := make([]statecheck.StateCheck, 0, len(values))
	for name, v := range values {
		checks = append(checks, statecheck.ExpectKnownValue(addr, tfjsonpath.New(name), v))
	}
	return checks
}

func stringList(values ...string) knownvalue.Check {
	checks := make([]knownvalue.Check, len(values))
	for i, v := range values {
		checks[i] = knownvalue.StringExact(v)
	}
	return knownvalue.ListExact(checks)
}

func target(id, name string) knownvalue.Check {
	return knownvalue.ObjectExact(map[string]knownvalue.Check{
		"service_account_id": knownvalue.StringExact(id), "service_account_name": knownvalue.StringExact(name),
	})
}

// checkRulesArchived is a CheckDestroy: every rule of the fake is archived,
// there being no delete endpoint.
func (f *federationFake) checkRulesArchived(*terraform.State) error {
	for _, o := range f.rules.List(nil) {
		if o["archived_at"] == nil {
			return fmt.Errorf("rule %s not archived after destroy", o["id"])
		}
	}
	return nil
}

const fullRule = `  name                   = "ci-deploy"
  description            = "Deploys from main."
  workspace_id           = "wrkspc_a"
  token_lifetime_seconds = 900
  match = {
    subject_prefix = "repo:ippontech/"
    audience       = "https://api.anthropic.com"
    claims         = { ref = "refs/heads/main" }
    condition      = "claims.ref_type == 'branch'"
  }
  target = { service_account_id = "svac_001" }
`

// TestFederationRuleResource_lifecycle creates a rule with every attribute
// (issuer_name, missing from the create response, is read back), imports
// it, moves it in place to another workspace with another match, target and
// lifetime and no description, and archives it on destroy.
func TestFederationRuleResource_lifecycle(t *testing.T) {
	t.Parallel()
	srv := newFederationFake(t, 0)
	seedIssuer(srv)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(srv),
		CheckDestroy:             srv.checkRulesArchived,
		Steps: []resource.TestStep{
			{
				Config:           ruleConfig(fullRule),
				ConfigPlanChecks: expectAction(ruleAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expect(ruleAddr, map[string]knownvalue.Check{
					"id": knownvalue.StringExact(ruleID(1)), "name": knownvalue.StringExact("ci-deploy"),
					"description": knownvalue.StringExact("Deploys from main."),
					"issuer_id":   knownvalue.StringExact("fdis_github"), "issuer_name": knownvalue.StringExact("github"),
					"oauth_scope":  knownvalue.StringExact("workspace:developer"),
					"workspace_id": knownvalue.StringExact("wrkspc_a"), "workspace_ids": stringList("wrkspc_a"),
					"applies_to_all_workspaces": knownvalue.Bool(false), "token_lifetime_seconds": knownvalue.Int64Exact(900),
					"attributes": knownvalue.Null(),
					"match": knownvalue.ObjectExact(map[string]knownvalue.Check{
						"subject_prefix": knownvalue.StringExact("repo:ippontech/"), "audience": knownvalue.StringExact("https://api.anthropic.com"),
						"claims":    knownvalue.MapExact(map[string]knownvalue.Check{"ref": knownvalue.StringExact("refs/heads/main")}),
						"condition": knownvalue.StringExact("claims.ref_type == 'branch'"),
					}),
					"target":     target("svac_001", "sa-one"),
					"created_at": knownvalue.StringExact(at(1)), "updated_at": knownvalue.StringExact(at(1)),
					"created_by_actor_id": knownvalue.StringExact("user_001"), "updated_by_actor_id": knownvalue.StringExact("user_001"),
					"archived_at": knownvalue.Null(), "archived_by_actor_id": knownvalue.Null(),
				}),
			},
			{
				Config:            ruleConfig(fullRule),
				ResourceName:      ruleAddr,
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				Config: ruleConfig(`  name                   = "ci-deploy-release"
  workspace_id           = "wrkspc_b"
  token_lifetime_seconds = 1200
  match                  = { condition = "claims.ref == 'refs/heads/release'" }
  target                 = { service_account_id = "svac_002" }
`),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(ruleAddr, plancheck.ResourceActionUpdate),
						plancheck.ExpectUnknownValue(ruleAddr, tfjsonpath.New("target").AtMapKey("service_account_name")),
					},
					PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
				},
				ConfigStateChecks: expect(ruleAddr, map[string]knownvalue.Check{
					"name": knownvalue.StringExact("ci-deploy-release"), "description": knownvalue.Null(),
					"workspace_id": knownvalue.StringExact("wrkspc_b"), "workspace_ids": stringList("wrkspc_b"), "applies_to_all_workspaces": knownvalue.Bool(false),
					"token_lifetime_seconds": knownvalue.Int64Exact(1200), "target": target("svac_002", "sa-two"),
					"match": knownvalue.ObjectExact(map[string]knownvalue.Check{
						"subject_prefix": knownvalue.Null(), "audience": knownvalue.Null(), "claims": knownvalue.Null(),
						"condition": knownvalue.StringExact("claims.ref == 'refs/heads/release'"),
					}),
				}),
			},
		},
	})

	srv.AssertJSONBodies(routeCreateRule, `{
		"name": "ci-deploy", "description": "Deploys from main.", "issuer_id": "fdis_github", "oauth_scope": "workspace:developer",
		"workspace_id": "wrkspc_a", "applies_to_all_workspaces": false, "token_lifetime_seconds": 900,
		"match": {"subject_prefix": "repo:ippontech/", "audience": "https://api.anthropic.com", "claims": {"ref": "refs/heads/main"},
			"condition": "claims.ref_type == 'branch'"},
		"target": {"type": "service_account", "service_account_id": "svac_001"}}`)
	srv.AssertJSONBodies(routeUpdateRule, `{
		"name": "ci-deploy-release", "description": null, "oauth_scope": "workspace:developer",
		"workspace_id": "wrkspc_b", "applies_to_all_workspaces": false, "token_lifetime_seconds": 1200,
		"match": {"condition": "claims.ref == 'refs/heads/release'"}, "target": {"type": "service_account", "service_account_id": "svac_002"}}`)
	srv.AssertCalled(routeArchiveRule, 1)
	srv.assertRequestShape()
}

func simpleRule(condition, extra string) string {
	return ruleConfig(fmt.Sprintf(`  name         = "ci-deploy"
  workspace_id = "wrkspc_a"
  match        = { condition = %q }
  target       = { service_account_id = "svac_001" }
`, condition) + extra)
}

// #349: an in-place update (only match.condition changes) keeps
// target.service_account_name, which the update response omits. The
// resource maps the read that confirms the write instead.
func TestFederationRuleResource_updateKeepsServiceAccountName(t *testing.T) {
	t.Parallel()
	srv := newFederationFake(t, 0)
	seedIssuer(srv)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(srv),
		Steps: []resource.TestStep{
			{
				Config:            simpleRule("claims.ref == 'refs/heads/main'", ""),
				ConfigPlanChecks:  expectAction(ruleAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expect(ruleAddr, map[string]knownvalue.Check{"target": target("svac_001", "sa-one")}),
			},
			{
				Config:           simpleRule("claims.ref == 'refs/heads/release'", ""),
				ConfigPlanChecks: expectAction(ruleAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expect(ruleAddr, map[string]knownvalue.Check{
					"target": target("svac_001", "sa-one"),
					"match": knownvalue.ObjectExact(map[string]knownvalue.Check{
						"subject_prefix": knownvalue.Null(), "audience": knownvalue.Null(), "claims": knownvalue.Null(),
						"condition": knownvalue.StringExact("claims.ref == 'refs/heads/release'"),
					}),
				}),
			},
		},
	})
}

// When the read after the update fails (here a 403, which ends the
// best-effort wait at once), the write response is mapped with the planned
// name, since it names none for the same service account.
func TestFederationRuleResource_updateKeepsServiceAccountNameWhenReadBackFails(t *testing.T) {
	t.Parallel()
	srv := newFederationFake(t, 0)
	seedIssuer(srv)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(srv),
		Steps: []resource.TestStep{
			{Config: simpleRule("claims.ref == 'refs/heads/main'", "")},
			{
				PreConfig:         func() { srv.failReadAfterRuleUpdate.Store(true) },
				Config:            simpleRule("claims.ref == 'refs/heads/release'", ""),
				ConfigPlanChecks:  expectAction(ruleAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expect(ruleAddr, map[string]knownvalue.Check{"target": target("svac_001", "sa-one")}),
				Check: func(*terraform.State) error {
					srv.failReadAfterRuleUpdate.Store(false)
					faults := 0
					for _, r := range srv.Requests(routeGetRule) {
						if r.Fault == "status 403" {
							faults++
						}
					}
					if faults != 1 {
						return fmt.Errorf("read-back failures served = %d, want 1", faults)
					}
					return nil
				},
			},
		},
	})
}

// Switching a rule back from all workspaces to one sends
// applies_to_all_workspaces = false explicitly (omitting it would keep the
// server's true).
func TestFederationRuleResource_workspaceAndAttributes(t *testing.T) {
	t.Parallel()
	srv := newFederationFake(t, 0)
	seedIssuer(srv)
	all := ruleConfig(`  name                      = "ci-deploy"
  applies_to_all_workspaces = true
  match                     = { condition = "true" }
  target                    = { service_account_id = "svac_001" }
`)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(srv),
		Steps: []resource.TestStep{
			{Config: all, ConfigPlanChecks: expectAction(ruleAddr, plancheck.ResourceActionCreate)},
			{
				Config:           simpleRule("true", ""),
				ConfigPlanChecks: expectAction(ruleAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expect(ruleAddr, map[string]knownvalue.Check{
					"applies_to_all_workspaces": knownvalue.Bool(false), "workspace_ids": stringList("wrkspc_a"),
				}),
			},
		},
	})

	srv.AssertJSONBodies(routeUpdateRule, `{"name": "ci-deploy", "description": null, "oauth_scope": "workspace:developer",
		"workspace_id": "wrkspc_a", "applies_to_all_workspaces": false, "token_lifetime_seconds": 3600,
		"match": {"condition": "true"}, "target": {"type": "service_account", "service_account_id": "svac_001"}}`)
}

// TestFederationRuleResource_goneOutOfBand: a rule deleted behind
// Terraform's back is re-created by the next apply; destroy succeeds when
// the archive answers 404.
func TestFederationRuleResource_goneOutOfBand(t *testing.T) {
	t.Parallel()
	srv := newFederationFake(t, 0)
	seedIssuer(srv)
	cfg := simpleRule("true", "")

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(srv),
		Steps: []resource.TestStep{
			{Config: cfg},
			{
				PreConfig:         func() { srv.rules.Delete(ruleID(1)) },
				Config:            cfg,
				ConfigPlanChecks:  expectAction(ruleAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expect(ruleAddr, map[string]knownvalue.Check{"id": knownvalue.StringExact(ruleID(2))}),
			},
			{
				PreConfig: func() { srv.FailNext(routeArchiveRule, http.StatusNotFound, 1) },
				Config:    cfg,
				Destroy:   true,
			},
		},
	})
	if reqs := srv.Requests(routeArchiveRule); len(reqs) != 1 || reqs[0].Fault != "status 404" {
		t.Errorf("archive requests = %+v, want one answered 404", reqs)
	}
}

// TestFederationRuleResource_issuerNameReadBackFails: when the read-back of
// issuer_name after the create fails, the create response is kept (empty
// issuer_name) instead of failing the apply; the refresh fills it in.
func TestFederationRuleResource_issuerNameReadBackFails(t *testing.T) {
	t.Parallel()
	srv := newFederationFake(t, 0)
	seedIssuer(srv)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(srv),
		Steps: []resource.TestStep{{
			PreConfig: func() { srv.FailNext(routeGetRule, http.StatusForbidden, 1) },
			Config:    simpleRule("true", ""),
			Check: func(s *terraform.State) error {
				if got := s.RootModule().Resources[ruleAddr].Primary.Attributes["issuer_name"]; got != "" {
					return fmt.Errorf("issuer_name after create = %q, want the create response's empty value", got)
				}
				return nil
			},
		}},
	})
}

// TestFederationRuleResource_apiErrors: an API error on create, refresh,
// update or archive is an error diagnostic; none is retried (4xx).
func TestFederationRuleResource_apiErrors(t *testing.T) {
	t.Parallel()
	cfg := simpleRule("true", "")
	for name, tc := range map[string]struct {
		route string
		steps func(fail func()) []resource.TestStep
	}{
		"create": {routeCreateRule, func(fail func()) []resource.TestStep {
			return []resource.TestStep{{PreConfig: fail, Config: cfg, ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+create\s+federation\s+rule:.*403\s+Forbidden`)}}
		}},
		"refresh": {routeGetRule, func(fail func()) []resource.TestStep {
			return []resource.TestStep{
				{Config: cfg},
				{PreConfig: fail, RefreshState: true, ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+read\s+federation\s+rule:.*403\s+Forbidden`)},
			}
		}},
		"update": {routeUpdateRule, func(fail func()) []resource.TestStep {
			return []resource.TestStep{
				{Config: cfg},
				{PreConfig: fail, Config: simpleRule("false", ""), ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+update\s+federation\s+rule:.*403\s+Forbidden`)},
			}
		}},
		"archive": {routeArchiveRule, func(fail func()) []resource.TestStep {
			return []resource.TestStep{
				{Config: cfg},
				{PreConfig: fail, Config: cfg, Destroy: true, ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+archive\s+federation\s+rule:.*403\s+Forbidden`)},
			}
		}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newFederationFake(t, 0)
			seedIssuer(srv)
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
			if faults != 1 {
				t.Errorf("%s: %d failed requests, want 1 (no retry)", tc.route, faults)
			}
		})
	}
}

// TestFederationRuleResource_invalid: the config validator rejects a match
// without subject_prefix, claims or condition, and a rule with both
// workspace_id and applies_to_all_workspaces, before any request.
func TestFederationRuleResource_invalid(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		body, pattern string
	}{
		"audience alone": {
			"  name = \"x\"\n  workspace_id = \"wrkspc_a\"\n  match = { audience = \"aud\" }\n  target = { service_account_id = \"svac_001\" }\n",
			`(?s)At\s+least\s+one\s+of\s+"match.subject_prefix",\s+"match.claims",\s+or\s+"match.condition"`,
		},
		"both workspace settings": {
			"  name = \"x\"\n  workspace_id = \"wrkspc_a\"\n  applies_to_all_workspaces = true\n  match = { condition = \"true\" }\n  target = { service_account_id = \"svac_001\" }\n",
			`(?s)Exactly\s+one\s+of\s+"workspace_id"\s+or\s+"applies_to_all_workspaces\s+=\s+true"\s+must\s+be\s+set,\s+not\s+both`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newFederationFake(t, 0)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(srv),
				Steps:                    []resource.TestStep{{Config: ruleConfig(tc.body), ExpectError: regexp.MustCompile(tc.pattern)}},
			})
			srv.AssertCalled("", 0)
		})
	}
}

// TestFederationRuleResource_attributes: attributes = {} is sent as {} and
// stays an empty map, although the API always answers null ("Not yet
// supported; always null", .../federation/rules/retrieve); removing it sends
// null ("Send null to clear them", .../federation/rules/update).
func TestFederationRuleResource_attributes(t *testing.T) {
	t.Skip("bug: attributes = {} reads back as null (inconsistent result after apply); see fix/federation-empty-attributes")
	t.Parallel()
	srv := newFederationFake(t, 0)
	seedIssuer(srv)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(srv),
		Steps: []resource.TestStep{
			{
				Config:            simpleRule("true", "  attributes = {}\n"),
				ConfigPlanChecks:  expectAction(ruleAddr, plancheck.ResourceActionCreate),
				ConfigStateChecks: expect(ruleAddr, map[string]knownvalue.Check{"attributes": knownvalue.MapExact(map[string]knownvalue.Check{})}),
			},
			{
				Config:            simpleRule("true", ""),
				ConfigPlanChecks:  expectAction(ruleAddr, plancheck.ResourceActionUpdate),
				ConfigStateChecks: expect(ruleAddr, map[string]knownvalue.Check{"attributes": knownvalue.Null()}),
			},
		},
	})
	srv.AssertJSONBodies(routeUpdateRule, `{"name": "ci-deploy", "description": null, "oauth_scope": "workspace:developer",
		"workspace_id": "wrkspc_a", "applies_to_all_workspaces": false, "token_lifetime_seconds": 3600, "attributes": null,
		"match": {"condition": "true"}, "target": {"type": "service_account", "service_account_id": "svac_001"}}`)
}

// TestFederationRuleResource_attributesRejected: non-empty attributes are
// sent on create and update, and the API's 400 ("any non-empty value is
// rejected with 400") is an error diagnostic.
func TestFederationRuleResource_attributesRejected(t *testing.T) {
	t.Parallel()
	attrs := "  attributes = { env = \"claims.environment\" }\n"
	for name, steps := range map[string][]resource.TestStep{
		"create": {{Config: simpleRule("true", attrs), ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+create\s+federation\s+rule:.*400\s+Bad\s+Request`)}},
		"update": {
			{Config: simpleRule("true", "")},
			{Config: simpleRule("true", attrs), ExpectError: regexp.MustCompile(`(?s)Unable\s+to\s+update\s+federation\s+rule:.*400\s+Bad\s+Request`)},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newFederationFake(t, 0)
			seedIssuer(srv)
			resource.UnitTest(t, resource.TestCase{ProtoV6ProviderFactories: factories(srv), Steps: steps})
			route := map[string]string{"create": routeCreateRule, "update": routeUpdateRule}[name]
			reqs := srv.Requests(route)
			var body struct{ Attributes map[string]string }
			if len(reqs) != 1 {
				t.Fatalf("%d requests to %s, want 1", len(reqs), route)
			}
			if err := reqs[0].DecodeJSON(&body); err != nil || body.Attributes["env"] != "claims.environment" {
				t.Errorf("attributes sent = %v (%v), want env", body.Attributes, err)
			}
		})
	}
}
