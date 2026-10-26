// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package deployments_test

import (
	"encoding/json"
	"maps"
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

// A deployment configured with paused = true is created, then paused: the
// create endpoint has no paused field and answers an active deployment
// (https://platform.claude.com/docs/en/api/beta/deployments/create), and the
// pause endpoint answers it paused with a manual paused_reason
// (https://platform.claude.com/docs/en/api/beta/deployments/pause).
func TestDeploymentResource_createdPaused(t *testing.T) {
	t.Parallel()
	const (
		create  = "POST /v1/deployments"
		pause   = "POST /v1/deployments/{deployment_id}/pause"
		get     = "GET /v1/deployments/{deployment_id}"
		archive = "POST /v1/deployments/{deployment_id}/archive"
	)
	srv := sdktest.NewServer(t)
	store := srv.NewCollection("depl_")
	srv.Handle(create, func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			sdktest.WriteAPIError(w, http.StatusBadRequest, "", err.Error())
			return
		}
		body["agent"] = map[string]any{"type": "agent", "id": body["agent"], "version": 1}
		maps.Copy(body, map[string]any{
			"type": "deployment", "status": "active", "paused_reason": nil, "archived_at": nil, "description": nil,
			"metadata": map[string]any{}, "resources": []any{}, "vault_ids": []any{}, "schedule": nil,
		})
		sdktest.WriteJSON(w, http.StatusOK, store.Create(body))
	})
	srv.Handle(pause, func(w http.ResponseWriter, r *http.Request) {
		o, ok := store.Update(r.PathValue("deployment_id"), map[string]any{"status": "paused", "paused_reason": map[string]any{"type": "manual"}}, sdktest.Replace)
		if !ok {
			sdktest.WriteAPIError(w, http.StatusNotFound, "", "deployment not found")
			return
		}
		sdktest.WriteJSON(w, http.StatusOK, o)
	})
	store.HandleGet(get)
	store.HandleArchive(archive)
	addr := "anthropic_deployment.test"

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv),
		Steps: []resource.TestStep{{
			Config: providertest.ProviderConfig(sdktest.APIKey) + `
resource "anthropic_deployment" "test" {
  name           = "paused-from-the-start"
  agent_id       = "agent_1"
  environment_id = "env_1"
  initial_events = jsonencode([{ type = "user.message", content = [{ type = "text", text = "Go." }] }])
  paused         = true
}
`,
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, plancheck.ResourceActionCreate)},
				PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
			},
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("paused"), knownvalue.Bool(true)),
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("status"), knownvalue.StringExact("paused")),
			},
		}},
	})

	srv.AssertCalled(create, 1)
	srv.AssertCalled(pause, 1)
	srv.AssertCalled(archive, 1)
	srv.AssertBeta(pause, "managed-agents-2026-04-01")
	srv.AssertCredential(pause, sdktest.APIKey)
}
