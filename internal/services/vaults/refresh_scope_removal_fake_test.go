// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package vaults_test

import (
	"encoding/json"
	"maps"
	"net/http"
	"strconv"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// newRefreshScopeFake serves the vault and mcp_oauth credential endpoints
// the test needs. The update applies refresh field by field: an omitted
// field is kept and a scope of "" clears the stored one ("Send an empty
// string to clear the stored scope",
// https://platform.claude.com/docs/en/api/beta/vaults/credentials/update);
// secrets are never returned.
func newRefreshScopeFake(t *testing.T) *sdktest.Server {
	t.Helper()
	srv := sdktest.NewServer(t)
	vaultStore, credentialStore := srv.NewCollection("vlt_"), srv.NewCollection("vcrd_")
	decode := func(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			sdktest.WriteAPIError(w, http.StatusBadRequest, "", err.Error())
			return nil, false
		}
		return body, true
	}
	srv.Handle("POST /v1/vaults", func(w http.ResponseWriter, r *http.Request) {
		if body, ok := decode(w, r); ok {
			obj := map[string]any{"type": "vault", "metadata": map[string]any{}, "archived_at": nil}
			maps.Copy(obj, body)
			sdktest.WriteJSON(w, http.StatusOK, vaultStore.Create(obj))
		}
	})
	vaultStore.HandleGet("GET /v1/vaults/{vault_id}")
	vaultStore.HandleDelete("DELETE /v1/vaults/{vault_id}", "vault_deleted")
	srv.Handle("POST /v1/vaults/{vault_id}/credentials", func(w http.ResponseWriter, r *http.Request) {
		if body, ok := decode(w, r); ok {
			auth := body["auth"].(map[string]any)
			refresh := auth["refresh"].(map[string]any)
			sdktest.WriteJSON(w, http.StatusOK, credentialStore.Create(map[string]any{
				"type": "vault_credential", "vault_id": r.PathValue("vault_id"), "metadata": map[string]any{}, "archived_at": nil,
				"auth": map[string]any{
					"type": "mcp_oauth", "mcp_server_url": auth["mcp_server_url"],
					"refresh": map[string]any{
						"client_id": refresh["client_id"], "token_endpoint": refresh["token_endpoint"], "scope": refresh["scope"],
						"token_endpoint_auth": map[string]any{"type": "none"},
					},
				},
			}))
		}
	})
	credentialStore.HandleGet("GET /v1/vaults/{vault_id}/credentials/{credential_id}")
	srv.Handle("POST /v1/vaults/{vault_id}/credentials/{credential_id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("credential_id")
		stored, found := credentialStore.Get(id)
		body, ok := decode(w, r)
		if !found || !ok {
			sdktest.WriteAPIError(w, http.StatusNotFound, "", "credential not found")
			return
		}
		auth := stored["auth"].(map[string]any)
		refresh := auth["refresh"].(map[string]any)
		if update, ok := body["auth"].(map[string]any)["refresh"].(map[string]any); ok {
			if scope, ok := update["scope"]; ok {
				refresh["scope"] = scope
				if scope == "" {
					delete(refresh, "scope")
				}
			}
		}
		obj, _ := credentialStore.Update(id, map[string]any{"auth": auth}, sdktest.Replace)
		sdktest.WriteJSON(w, http.StatusOK, obj)
	})
	credentialStore.HandleDelete("DELETE /v1/vaults/{vault_id}/credentials/{credential_id}", "vault_credential_deleted")
	return srv
}

// Removing refresh.scope from an mcp_oauth credential clears it: the update
// sends "scope": "", since the API keeps an omitted scope. A refresh update
// that keeps the scope out of the configuration sends no scope.
func TestVaultCredentialResource_refreshScopeRemoved(t *testing.T) {
	t.Parallel()
	srv := newRefreshScopeFake(t)
	const addr = "anthropic_vault_credential.test"
	cfg := func(version int, scope string) string {
		return providertest.ProviderConfig(sdktest.APIKey) + `
resource "anthropic_vault" "test" {
  display_name = "refresh scope"
}

resource "anthropic_vault_credential" "test" {
  vault_id         = anthropic_vault.test.id
  type             = "mcp_oauth"
  mcp_server_url   = "https://mcp.example.com/sse"
  access_token     = "access-token"
  token_wo_version = ` + strconv.Itoa(version) + `
  refresh = {
    client_id           = "client-1"
    refresh_token       = "refresh-token"
    token_endpoint      = "https://auth.example.com/token"
    token_endpoint_auth = { type = "none" }
` + scope + `  }
}
`
	}
	step := func(config string, action plancheck.ResourceActionType, scope knownvalue.Check) resource.TestStep {
		return resource.TestStep{
			Config: config,
			ConfigPlanChecks: resource.ConfigPlanChecks{
				PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, action)},
				PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
			},
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownValue(addr, tfjsonpath.New("refresh").AtMapKey("scope"), scope),
			},
		}
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv),
		Steps: []resource.TestStep{
			step(cfg(1, "    scope               = \"read\"\n"), plancheck.ResourceActionCreate, knownvalue.StringExact("read")),
			step(cfg(1, ""), plancheck.ResourceActionUpdate, knownvalue.Null()),
			step(cfg(2, ""), plancheck.ResourceActionUpdate, knownvalue.Null()),
		},
	})

	srv.AssertJSONBodies("POST /v1/vaults/{vault_id}/credentials/{credential_id}",
		`{"display_name": null, "auth": {"type": "mcp_oauth", "access_token": "access-token", "refresh": {"refresh_token": "refresh-token", "scope": ""}}}`,
		`{"display_name": null, "auth": {"type": "mcp_oauth", "access_token": "access-token", "refresh": {"refresh_token": "refresh-token"}}}`)
}
