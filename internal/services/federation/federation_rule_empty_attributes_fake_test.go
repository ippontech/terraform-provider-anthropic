// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation_test

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// newEmptyAttributesFake serves the federation rule endpoints the test
// needs. The API answers attributes as null whatever the request held ("Not
// yet supported; always null",
// https://platform.claude.com/docs/en/api/beta/organization/federation/rules/retrieve).
func newEmptyAttributesFake(t *testing.T) *sdktest.Server {
	t.Helper()
	srv := sdktest.NewServer(t)
	rules := srv.NewCollection("fdrl_")
	write := func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			sdktest.WriteAPIError(w, http.StatusBadRequest, "", err.Error())
			return
		}
		rule := map[string]any{
			"type": "federation_rule", "name": body["name"], "issuer_id": "fdis_1", "issuer_name": "github",
			"oauth_scope": body["oauth_scope"], "match": body["match"], "workspace_id": body["workspace_id"],
			"workspace_ids": []any{body["workspace_id"]}, "applies_to_all_workspaces": false, "token_lifetime_seconds": 3600,
			"target":     map[string]any{"type": "service_account", "service_account_id": "svac_001", "service_account_name": "sa-one"},
			"attributes": nil, "archived_at": nil,
		}
		if id := r.PathValue("id"); id != "" {
			rule["id"] = id
			obj, _ := rules.Update(id, rule, sdktest.Replace)
			sdktest.WriteJSON(w, http.StatusOK, obj)
			return
		}
		sdktest.WriteJSON(w, http.StatusOK, rules.Create(rule))
	}
	srv.Handle("POST /v1/organizations/federation_rules", write)
	srv.Handle("POST /v1/organizations/federation_rules/{id}", write)
	rules.HandleGet("GET /v1/organizations/federation_rules/{id}")
	rules.HandleArchive("POST /v1/organizations/federation_rules/{id}/archive")
	return srv
}

// attributes = {} on anthropic_federation_rule is sent as {} and stays an
// empty map in state, although the API answers null; removing it then sends
// null ("Send null to clear them",
// https://platform.claude.com/docs/en/api/beta/organization/federation/rules/update).
func TestFederationRuleResource_emptyAttributesKept(t *testing.T) {
	t.Parallel()
	srv := newEmptyAttributesFake(t)
	const addr = "anthropic_federation_rule.test"
	cfg := func(attributes string) string {
		return providertest.ProviderConfig(sdktest.AuthToken) + `
resource "anthropic_federation_rule" "test" {
  name         = "ci-deploy"
  issuer_id    = "fdis_1"
  oauth_scope  = "workspace:developer"
  workspace_id = "wrkspc_a"
  match        = { condition = "true" }
  target       = { service_account_id = "svac_001" }
` + attributes + `}
`
	}
	step := func(config string, action plancheck.ResourceActionType, attributes knownvalue.Check) resource.TestStep {
		return resource.TestStep{
			Config: config,
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, action)},
				PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
			},
			ConfigStateChecks: []statecheck.StateCheck{statecheck.ExpectKnownValue(addr, tfjsonpath.New("attributes"), attributes)},
		}
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv),
		Steps: []resource.TestStep{
			step(cfg("  attributes = {}\n"), plancheck.ResourceActionCreate, knownvalue.MapExact(map[string]knownvalue.Check{})),
			step(cfg(""), plancheck.ResourceActionUpdate, knownvalue.Null()),
		},
	})

	var created struct{ Attributes map[string]string }
	if err := srv.Requests("POST /v1/organizations/federation_rules")[0].DecodeJSON(&created); err != nil || created.Attributes == nil {
		t.Errorf("create attributes = %v (%v), want {}", created.Attributes, err)
	}
	var updated map[string]any
	if err := srv.Requests("POST /v1/organizations/federation_rules/{id}")[0].DecodeJSON(&updated); err != nil || updated["attributes"] != nil {
		t.Errorf("update body = %v (%v), want attributes null", updated, err)
	} else if _, sent := updated["attributes"]; !sent {
		t.Errorf("update body = %v, want attributes sent as null", updated)
	}
}
