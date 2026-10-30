// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation_test

import (
	"context"
	"net/http"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/federation"
)

// seedIssuers stores three issuers as the retrieve reference shows them
// (https://platform.claude.com/docs/en/api/beta/organization/federation/issuers/retrieve):
// a discovery issuer with a paused poller, an archived explicit-URL issuer,
// and an inline-key issuer without poll status.
func seedIssuers(f *federationFake) {
	base := func(id string, n int) map[string]any {
		return map[string]any{
			"type": "federation_issuer", "id": id, "name": "issuer-" + id, "issuer_url": "https://" + id + ".example.com",
			"check_jti": true, "max_jwt_lifetime_seconds": 3600, "jwks_polling_disabled_at": nil, "poll_status": nil,
			"created_at": at(n), "updated_at": at(n + 1), "created_by_actor_id": "user_001", "updated_by_actor_id": "user_002",
			"archived_at": nil, "archived_by_actor_id": nil,
		}
	}
	discovery := base("fdis_a", 1)
	discovery["jwks"] = map[string]any{"type": "discovery", "discovery_base": "https://fdis_a.example.com/base"}
	discovery["jwks_polling_disabled_at"] = at(3)
	discovery["poll_status"] = map[string]any{"consecutive_failures": 4, "last_fetched_at": at(2), "next_poll_at": at(5)}
	explicit := base("fdis_b", 11)
	explicit["jwks"] = map[string]any{"type": "explicit_url", "url": "https://fdis_b.example.com/keys", "ca_cert_pem": "PEM"}
	explicit["archived_at"], explicit["archived_by_actor_id"] = at(13), "user_003"
	inline := base("fdis_c", 21)
	inline["check_jti"], inline["max_jwt_lifetime_seconds"] = false, 600
	inline["jwks"] = map[string]any{"type": "inline", "keys": []any{map[string]any{"kty": "RSA", "kid": "k1"}}}
	for _, o := range []map[string]any{discovery, explicit, inline} {
		f.issuers.Put(o)
	}
}

// issuerObject is what the data sources read for a seeded issuer. With no
// poll status they keep their released all-zero object (pollStatusObject),
// unlike the resource, which maps it to null.
func issuerObject(id string) knownvalue.Check {
	values := map[string]knownvalue.Check{
		"id": knownvalue.StringExact(id), "name": knownvalue.StringExact("issuer-" + id),
		"issuer_url": knownvalue.StringExact("https://" + id + ".example.com"),
		"check_jti":  knownvalue.Bool(true), "max_jwt_lifetime_seconds": knownvalue.Int64Exact(3600),
		"jwks_polling_disabled_at": knownvalue.Null(),
		"poll_status": knownvalue.ObjectExact(map[string]knownvalue.Check{
			"consecutive_failures": knownvalue.Int64Exact(0), "last_fetched_at": knownvalue.Null(), "next_poll_at": knownvalue.Null(),
		}),
		"created_by_actor_id": knownvalue.StringExact("user_001"), "updated_by_actor_id": knownvalue.StringExact("user_002"),
		"archived_at": knownvalue.Null(), "archived_by_actor_id": knownvalue.Null(),
	}
	switch id {
	case "fdis_a":
		values["jwks"] = jwks("discovery", map[string]knownvalue.Check{"discovery_base": knownvalue.StringExact("https://fdis_a.example.com/base")})
		values["jwks_polling_disabled_at"] = knownvalue.StringExact(at(3))
		values["poll_status"] = knownvalue.ObjectExact(map[string]knownvalue.Check{
			"consecutive_failures": knownvalue.Int64Exact(4), "last_fetched_at": knownvalue.StringExact(at(2)), "next_poll_at": knownvalue.StringExact(at(5)),
		})
		values["created_at"], values["updated_at"] = knownvalue.StringExact(at(1)), knownvalue.StringExact(at(2))
	case "fdis_b":
		values["jwks"] = jwks("explicit_url", map[string]knownvalue.Check{
			"url": knownvalue.StringExact("https://fdis_b.example.com/keys"), "ca_cert_pem": knownvalue.StringExact("PEM"),
		})
		values["archived_at"], values["archived_by_actor_id"] = knownvalue.StringExact(at(13)), knownvalue.StringExact("user_003")
		values["created_at"], values["updated_at"] = knownvalue.StringExact(at(11)), knownvalue.StringExact(at(12))
	case "fdis_c":
		values["jwks"] = jwks("inline", map[string]knownvalue.Check{"keys": knownvalue.StringExact(`[{"kid":"k1","kty":"RSA"}]`)})
		values["check_jti"], values["max_jwt_lifetime_seconds"] = knownvalue.Bool(false), knownvalue.Int64Exact(600)
		values["created_at"], values["updated_at"] = knownvalue.StringExact(at(21)), knownvalue.StringExact(at(22))
	}
	return knownvalue.ObjectExact(values)
}

// TestFederationIssuerDataSource_read reads each issuer variant.
func TestFederationIssuerDataSource_read(t *testing.T) {
	t.Parallel()
	srv := newFederationFake(t, 0)
	seedIssuers(srv)
	block := func(id string) string {
		return `
data "anthropic_federation_issuer" "` + id + `" {
  id = "` + id + `"
}

output "` + id + `" {
  value = data.anthropic_federation_issuer.` + id + `
}
`
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(srv),
		Steps: []resource.TestStep{{
			Config: config(block("fdis_a") + block("fdis_b") + block("fdis_c")),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownOutputValue("fdis_a", issuerObject("fdis_a")),
				statecheck.ExpectKnownOutputValue("fdis_b", issuerObject("fdis_b")),
				statecheck.ExpectKnownOutputValue("fdis_c", issuerObject("fdis_c")),
			},
		}},
	})
	srv.assertRequestShape()
}

// TestFederationIssuersDataSource_pages lists the issuers one per page
// through next_page; include_archived = true lists the archived one too.
func TestFederationIssuersDataSource_pages(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		extra string
		walk  []string
		want  []knownvalue.Check
	}{
		"live": {"", []string{"beta=true", "beta=true&page=" + sdktest.NextPageToken("fdis_a")}, []knownvalue.Check{issuerObject("fdis_a"), issuerObject("fdis_c")}},
		"include_archived": {
			"  include_archived = true\n",
			[]string{
				"beta=true&include_archived=true", "beta=true&include_archived=true&page=" + sdktest.NextPageToken("fdis_a"),
				"beta=true&include_archived=true&page=" + sdktest.NextPageToken("fdis_b"),
			},
			[]knownvalue.Check{issuerObject("fdis_a"), issuerObject("fdis_b"), issuerObject("fdis_c")},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newFederationFake(t, 1)
			seedIssuers(srv)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(srv),
				Steps: []resource.TestStep{{
					Config: config("data \"anthropic_federation_issuers\" \"test\" {\n" + tc.extra + "}\n"),
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue("data.anthropic_federation_issuers.test", tfjsonpath.New("issuers"), knownvalue.ListExact(tc.want)),
					},
				}},
			})
			srv.AssertWalks(routeListIssuers, tc.walk...)
			srv.AssertAllPagesRead(routeListIssuers)
			srv.assertRequestShape()
		})
	}
}

// seedRules stores two live rules of fdis_a, one of fdis_b, and an archived
// one, as the rules retrieve reference shows them.
func seedRules(f *federationFake) {
	for i, r := range []struct {
		id, issuer string
		archived   bool
	}{{"fdrl_1", "fdis_a", false}, {"fdrl_2", "fdis_b", false}, {"fdrl_3", "fdis_a", true}, {"fdrl_4", "fdis_a", false}} {
		o := map[string]any{
			"type": "federation_rule", "id": r.id, "name": "rule-" + r.id, "description": nil, "issuer_id": r.issuer, "issuer_name": "issuer-" + r.issuer,
			"match": map[string]any{"condition": "true"}, "oauth_scope": "workspace:inference",
			"target":       map[string]any{"type": "service_account", "service_account_id": "svac_001", "service_account_name": "sa-one"},
			"workspace_id": "wrkspc_a", "workspace_ids": []any{"wrkspc_a", "wrkspc_b"}, "applies_to_all_workspaces": false,
			"token_lifetime_seconds": 600, "attributes": nil,
			"created_at": at(10 * i), "updated_at": at(10*i + 1), "created_by_actor_id": "user_001", "updated_by_actor_id": "user_001",
			"archived_at": nil, "archived_by_actor_id": nil,
		}
		if r.id == "fdrl_1" {
			o["description"] = "First rule."
			o["match"] = map[string]any{"subject_prefix": "repo:x/", "audience": "aud", "claims": map[string]any{"ref": "main"}}
			o["workspace_id"], o["applies_to_all_workspaces"] = nil, true
		}
		if r.archived {
			o["archived_at"], o["archived_by_actor_id"] = at(10*i+2), "user_002"
		}
		f.rules.Put(o)
	}
}

func ruleObject(id string, n int, withType bool) knownvalue.Check {
	i := n - 1
	targetValues := map[string]knownvalue.Check{"service_account_id": knownvalue.StringExact("svac_001"), "service_account_name": knownvalue.StringExact("sa-one")}
	if withType {
		targetValues["type"] = knownvalue.StringExact("service_account")
	}
	issuer := map[string]string{"fdrl_1": "fdis_a", "fdrl_2": "fdis_b", "fdrl_3": "fdis_a", "fdrl_4": "fdis_a"}[id]
	values := map[string]knownvalue.Check{
		"id": knownvalue.StringExact(id), "name": knownvalue.StringExact("rule-" + id), "description": knownvalue.Null(),
		"issuer_id": knownvalue.StringExact(issuer), "issuer_name": knownvalue.StringExact("issuer-" + issuer),
		"match": knownvalue.ObjectExact(map[string]knownvalue.Check{
			"subject_prefix": knownvalue.Null(), "audience": knownvalue.Null(), "claims": knownvalue.Null(), "condition": knownvalue.StringExact("true"),
		}),
		"oauth_scope": knownvalue.StringExact("workspace:inference"), "target": knownvalue.ObjectExact(targetValues),
		"workspace_id": knownvalue.StringExact("wrkspc_a"), "workspace_ids": stringList("wrkspc_a", "wrkspc_b"),
		"applies_to_all_workspaces": knownvalue.Bool(false), "token_lifetime_seconds": knownvalue.Int64Exact(600),
		"attributes": knownvalue.Null(), "created_at": knownvalue.StringExact(at(10 * i)), "updated_at": knownvalue.StringExact(at(10*i + 1)),
		"created_by_actor_id": knownvalue.StringExact("user_001"), "updated_by_actor_id": knownvalue.StringExact("user_001"),
		"archived_at": knownvalue.Null(), "archived_by_actor_id": knownvalue.Null(),
	}
	if withType {
		values["type"] = knownvalue.StringExact("federation_rule")
	}
	if id == "fdrl_1" {
		values["description"] = knownvalue.StringExact("First rule.")
		values["match"] = knownvalue.ObjectExact(map[string]knownvalue.Check{
			"subject_prefix": knownvalue.StringExact("repo:x/"), "audience": knownvalue.StringExact("aud"),
			"claims": knownvalue.MapExact(map[string]knownvalue.Check{"ref": knownvalue.StringExact("main")}), "condition": knownvalue.Null(),
		})
		values["workspace_id"], values["applies_to_all_workspaces"] = knownvalue.Null(), knownvalue.Bool(true)
	}
	if id == "fdrl_3" {
		values["archived_at"], values["archived_by_actor_id"] = knownvalue.StringExact(at(10*i+2)), knownvalue.StringExact("user_002")
	}
	return knownvalue.ObjectExact(values)
}

// TestFederationRuleDataSource_read reads two rules by ID.
func TestFederationRuleDataSource_read(t *testing.T) {
	t.Parallel()
	srv := newFederationFake(t, 0)
	seedRules(srv)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(srv),
		Steps: []resource.TestStep{{
			Config: config(`
data "anthropic_federation_rule" "first" {
  id = "fdrl_1"
}

data "anthropic_federation_rule" "archived" {
  id = "fdrl_3"
}

output "first" {
  value = data.anthropic_federation_rule.first
}

output "archived" {
  value = data.anthropic_federation_rule.archived
}
`),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownOutputValue("first", ruleObject("fdrl_1", 1, false)),
				statecheck.ExpectKnownOutputValue("archived", ruleObject("fdrl_3", 3, false)),
			},
		}},
	})
	srv.assertRequestShape()
}

// TestFederationRulesDataSource_pages lists the rules two per page through
// next_page, with the issuer_id filter and include_archived sent as
// configured.
func TestFederationRulesDataSource_pages(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		extra string
		walk  []string
		want  []knownvalue.Check
	}{
		"live": {"", []string{"beta=true", "beta=true&page=" + sdktest.NextPageToken("fdrl_2")},
			[]knownvalue.Check{ruleObject("fdrl_1", 1, true), ruleObject("fdrl_2", 2, true), ruleObject("fdrl_4", 4, true)}},
		"issuer and archived": {
			"  issuer_id = \"fdis_a\"\n  include_archived = true\n",
			[]string{"beta=true&include_archived=true&issuer_id=fdis_a", "beta=true&include_archived=true&issuer_id=fdis_a&page=" + sdktest.NextPageToken("fdrl_3")},
			[]knownvalue.Check{ruleObject("fdrl_1", 1, true), ruleObject("fdrl_3", 3, true), ruleObject("fdrl_4", 4, true)},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newFederationFake(t, 2)
			seedRules(srv)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(srv),
				Steps: []resource.TestStep{{
					Config: config("data \"anthropic_federation_rules\" \"test\" {\n" + tc.extra + "}\n"),
					ConfigStateChecks: []statecheck.StateCheck{
						statecheck.ExpectKnownValue("data.anthropic_federation_rules.test", tfjsonpath.New("rules"), knownvalue.ListExact(tc.want)),
					},
				}},
			})
			srv.AssertWalks(routeListRules, tc.walk...)
			srv.AssertAllPagesRead(routeListRules)
		})
	}
}

// TestFederationRuleWorkspacesDataSource_pages lists the workspaces of a
// rule one per page, with their names.
func TestFederationRuleWorkspacesDataSource_pages(t *testing.T) {
	t.Parallel()
	srv := newFederationFake(t, 1)
	seedRule(srv, "fdrl_seeded")
	seedRule(srv, "fdrl_other")
	for i, ws := range []string{"wrkspc_a", "wrkspc_b"} {
		srv.workspaces.Put(map[string]any{
			"id": "fdrl_seeded:" + ws, "type": "federation_rule_workspace", "federation_rule_id": "fdrl_seeded",
			"workspace_id": ws, "workspace_name": fakeWorkspaceNames[ws], "created_at": at(i + 1), "created_by_actor_id": "user_001",
		})
	}
	srv.workspaces.Put(map[string]any{"id": "fdrl_other:wrkspc_a", "federation_rule_id": "fdrl_other", "workspace_id": "wrkspc_a"})
	workspace := func(id, name string, n int) knownvalue.Check {
		return knownvalue.ObjectExact(map[string]knownvalue.Check{
			"workspace_id": knownvalue.StringExact(id), "workspace_name": knownvalue.StringExact(name),
			"created_at": knownvalue.StringExact(at(n)), "created_by_actor_id": knownvalue.StringExact("user_001"),
		})
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(srv),
		Steps: []resource.TestStep{{
			Config: config(`data "anthropic_federation_rule_workspaces" "test" { federation_rule_id = "fdrl_seeded" }`),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.anthropic_federation_rule_workspaces.test", tfjsonpath.New("workspaces"),
					knownvalue.ListExact([]knownvalue.Check{workspace("wrkspc_a", "alpha", 1), workspace("wrkspc_b", "beta", 2)})),
			},
		}},
	})
	srv.AssertWalks(routeListWorkspaces, "beta=true", "beta=true&page="+sdktest.NextPageToken("fdrl_seeded:wrkspc_a"))
	srv.AssertAllPagesRead(routeListWorkspaces)
	srv.assertRequestShape()
}

// TestFederationDataSources_empty: no issuer, rule or enablement is an empty
// list.
func TestFederationDataSources_empty(t *testing.T) {
	t.Parallel()
	srv := newFederationFake(t, 0)
	seedRule(srv, "fdrl_seeded")
	srv.rules.Archive("fdrl_seeded")
	empty := knownvalue.ListExact([]knownvalue.Check{})

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: factories(srv),
		Steps: []resource.TestStep{{
			Config: config(`
data "anthropic_federation_issuers" "test" {}
data "anthropic_federation_rules" "test" {}
data "anthropic_federation_rule_workspaces" "test" { federation_rule_id = "fdrl_seeded" }
`),
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue("data.anthropic_federation_issuers.test", tfjsonpath.New("issuers"), empty),
				statecheck.ExpectKnownValue("data.anthropic_federation_rules.test", tfjsonpath.New("rules"), empty),
				statecheck.ExpectKnownValue("data.anthropic_federation_rule_workspaces.test", tfjsonpath.New("workspaces"), empty),
			},
		}},
	})
}

// TestFederationDataSources_errors: an unknown issuer or rule, and an API
// error on each list, are error diagnostics.
func TestFederationDataSources_errors(t *testing.T) {
	t.Parallel()
	for name, tc := range map[string]struct {
		block, route string
		fail         int
		pattern      string
	}{
		"issuer not found":  {`data "anthropic_federation_issuer" "test" { id = "fdis_missing" }`, routeGetIssuer, 0, `(?s)Unable\s+to\s+read\s+federation\s+issuer:.*404\s+Not\s+Found`},
		"rule not found":    {`data "anthropic_federation_rule" "test" { id = "fdrl_missing" }`, routeGetRule, 0, `(?s)Unable\s+to\s+retrieve\s+federation\s+rule:.*404\s+Not\s+Found`},
		"issuers forbidden": {`data "anthropic_federation_issuers" "test" {}`, routeListIssuers, http.StatusForbidden, `(?s)Unable\s+to\s+list\s+federation\s+issuers:.*403\s+Forbidden`},
		"rules forbidden":   {`data "anthropic_federation_rules" "test" {}`, routeListRules, http.StatusForbidden, `(?s)Unable\s+to\s+list\s+federation\s+rules:.*403\s+Forbidden`},
		"workspaces of an unknown rule": {
			`data "anthropic_federation_rule_workspaces" "test" { federation_rule_id = "fdrl_missing" }`, routeListWorkspaces, 0,
			`(?s)Unable\s+to\s+list\s+workspaces\s+for\s+federation\s+rule\s+"fdrl_missing":.*404\s+Not\s+Found`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newFederationFake(t, 0)
			if tc.fail != 0 {
				srv.FailNext(tc.route, tc.fail, 1)
			}
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(srv),
				Steps:                    []resource.TestStep{{Config: config(tc.block), ExpectError: regexp.MustCompile(tc.pattern)}},
			})
			srv.AssertCalled(tc.route, 1)
		})
	}
}

// TestFederation_requireOAuthToken: the three resources and five data
// sources refuse to run without an OAuth bearer, an API key alone being set,
// before any request.
func TestFederation_requireOAuthToken(t *testing.T) {
	t.Parallel()
	for name, block := range map[string]string{
		"anthropic_federation_issuer resource":         githubIssuer,
		"anthropic_federation_rule resource":           "",
		"anthropic_federation_rule_workspace resource": "",
		"anthropic_federation_issuer":                  `data "anthropic_federation_issuer" "test" { id = "fdis_x" }`,
		"anthropic_federation_issuers":                 `data "anthropic_federation_issuers" "test" {}`,
		"anthropic_federation_rule":                    `data "anthropic_federation_rule" "test" { id = "fdrl_x" }`,
		"anthropic_federation_rules":                   `data "anthropic_federation_rules" "test" {}`,
		"anthropic_federation_rule_workspaces":         `data "anthropic_federation_rule_workspaces" "test" { federation_rule_id = "fdrl_x" }`,
	} {
		switch name {
		case "anthropic_federation_issuer resource":
			block = issuerConfig(block)[len(config("")):]
		case "anthropic_federation_rule resource":
			block = simpleRule("true", "")[len(config("")):]
		case "anthropic_federation_rule_workspace resource":
			block = ruleWorkspaceConfig("wrkspc_a")[len(config("")):]
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newFederationFake(t, 0)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: factories(srv),
				Steps: []resource.TestStep{{
					Config:      providertest.ProviderConfig(sdktest.APIKey) + block,
					ExpectError: regexp.MustCompile(`Missing\s+OAuth\s+Token`),
				}},
			})
			srv.AssertCalled("", 0)
		})
	}
}

// TestFederation_rejectForeignProviderData covers the Configure guard
// Terraform cannot reach: provider data of another type is an error.
func TestFederation_rejectForeignProviderData(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for name, r := range map[string]fwresource.Resource{
		"anthropic_federation_issuer":         federation.NewFederationIssuerResource(),
		"anthropic_federation_rule":           federation.NewFederationRuleResource(),
		"anthropic_federation_rule_workspace": federation.NewFederationRuleWorkspaceResource(),
	} {
		var resp fwresource.ConfigureResponse
		r.(fwresource.ResourceWithConfigure).Configure(ctx, fwresource.ConfigureRequest{ProviderData: 42}, &resp)
		if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Unexpected Resource Configure Type" {
			t.Errorf("%s: Configure(int) diagnostics = %v, want Unexpected Resource Configure Type", name, resp.Diagnostics)
		}
	}
	for name, d := range map[string]datasource.DataSource{
		"anthropic_federation_issuer":          federation.NewFederationIssuerDataSource(),
		"anthropic_federation_issuers":         federation.NewFederationIssuersDataSource(),
		"anthropic_federation_rule":            federation.NewFederationRuleDataSource(),
		"anthropic_federation_rules":           federation.NewFederationRulesDataSource(),
		"anthropic_federation_rule_workspaces": federation.NewFederationRuleWorkspacesDataSource(),
	} {
		var resp datasource.ConfigureResponse
		d.(datasource.DataSourceWithConfigure).Configure(ctx, datasource.ConfigureRequest{ProviderData: 42}, &resp)
		if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Unexpected Data Source Configure Type" {
			t.Errorf("%s: Configure(int) diagnostics = %v, want Unexpected Data Source Configure Type", name, resp.Diagnostics)
		}
	}
}
