// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package vaults_test

import (
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

// newEmptyMetadataFake serves the vault and credential endpoints the test
// needs. A vault or a credential always comes back with metadata, {} when
// it has none ("metadata: map[string]" on
// https://platform.claude.com/docs/en/api/beta/vaults/retrieve and
// .../vaults/credentials/retrieve); the update applies metadata as a patch
// where null deletes a key (.../vaults/update).
func newEmptyMetadataFake(t *testing.T) *sdktest.Server {
	t.Helper()
	srv := sdktest.NewServer(t)
	vaultStore, credentialStore := srv.NewCollection("vlt_"), srv.NewCollection("vcrd_")
	srv.Handle("POST /v1/vaults", func(w http.ResponseWriter, r *http.Request) {
		if body, ok := srv.DecodeBody(w, r); ok {
			obj := map[string]any{"type": "vault", "metadata": map[string]any{}, "archived_at": nil}
			maps.Copy(obj, body)
			sdktest.WriteJSON(w, http.StatusOK, vaultStore.Create(obj))
		}
	})
	vaultStore.HandleGet("GET /v1/vaults/{vault_id}")
	vaultStore.HandleUpdate("POST /v1/vaults/{vault_id}", sdktest.Patch)
	vaultStore.HandleDelete("DELETE /v1/vaults/{vault_id}", "vault_deleted")
	srv.Handle("POST /v1/vaults/{vault_id}/credentials", func(w http.ResponseWriter, r *http.Request) {
		if body, ok := srv.DecodeBody(w, r); ok {
			auth := body["auth"].(map[string]any)
			obj := map[string]any{
				"type": "vault_credential", "vault_id": r.PathValue("vault_id"), "metadata": map[string]any{}, "archived_at": nil,
				"auth": map[string]any{"type": auth["type"], "mcp_server_url": auth["mcp_server_url"]},
			}
			if m, ok := body["metadata"]; ok {
				obj["metadata"] = m
			}
			sdktest.WriteJSON(w, http.StatusOK, credentialStore.Create(obj))
		}
	})
	credentialStore.HandleGet("GET /v1/vaults/{vault_id}/credentials/{credential_id}")
	credentialStore.HandleDelete("DELETE /v1/vaults/{vault_id}/credentials/{credential_id}", "vault_credential_deleted")
	return srv
}

// metadata = {} on anthropic_vault and anthropic_vault_credential is sent as
// {} and stays an empty map in state, although the API answers {} for no
// metadata too; emptying the metadata of a vault keeps {} as well (root
// CLAUDE.md, "Map attributes with PATCH semantics").
func TestVaultResources_emptyMetadataKept(t *testing.T) {
	t.Parallel()
	srv := newEmptyMetadataFake(t)
	cfg := func(vaultMetadata string) string {
		return providertest.ProviderConfig(sdktest.APIKey) + `
resource "anthropic_vault" "test" {
  display_name = "empty metadata"
  metadata     = ` + vaultMetadata + `
}

resource "anthropic_vault_credential" "test" {
  vault_id         = anthropic_vault.test.id
  type             = "static_bearer"
  mcp_server_url   = "https://mcp.example.com/mcp"
  token            = "bearer-token"
  token_wo_version = 1
  metadata         = {}
}
`
	}
	empty := knownvalue.MapExact(map[string]knownvalue.Check{})
	check := func(vaultMetadata knownvalue.Check) []statecheck.StateCheck {
		return []statecheck.StateCheck{
			statecheck.ExpectKnownValue("anthropic_vault.test", tfjsonpath.New("metadata"), vaultMetadata),
			statecheck.ExpectKnownValue("anthropic_vault_credential.test", tfjsonpath.New("metadata"), empty),
		}
	}
	actions := func(action plancheck.ResourceActionType) resource.ConfigPlanChecks {
		return providertest.ExpectAction("anthropic_vault.test", action)
	}

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv),
		Steps: []resource.TestStep{
			{Config: cfg("{}"), ConfigPlanChecks: actions(plancheck.ResourceActionCreate), ConfigStateChecks: check(empty)},
			{
				Config:            cfg(`{ team = "a" }`),
				ConfigPlanChecks:  actions(plancheck.ResourceActionUpdate),
				ConfigStateChecks: check(knownvalue.MapExact(map[string]knownvalue.Check{"team": knownvalue.StringExact("a")})),
			},
			{Config: cfg("{}"), ConfigPlanChecks: actions(plancheck.ResourceActionUpdate), ConfigStateChecks: check(empty)},
		},
	})

	srv.AssertJSONBodies("POST /v1/vaults", `{"display_name": "empty metadata", "metadata": {}}`)
	srv.AssertJSONBodies("POST /v1/vaults/{vault_id}",
		`{"display_name": "empty metadata", "metadata": {"team": "a"}}`,
		`{"display_name": "empty metadata", "metadata": {"team": null}}`)
	srv.AssertJSONBodies("POST /v1/vaults/{vault_id}/credentials", `{"metadata": {},
		"auth": {"type": "static_bearer", "mcp_server_url": "https://mcp.example.com/mcp", "token": "bearer-token"}}`)
}
