// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package vaults_test

import (
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// Routes of the vaults and vault credentials endpoints (beta
// managed-agents-2026-04-01, ?beta=true, standard key). References:
// https://platform.claude.com/docs/en/api/beta/vaults/create, .../list,
// .../retrieve, .../update, .../delete, .../archive, and the create,
// retrieve, update, delete and archive pages under
// https://platform.claude.com/docs/en/api/beta/vaults/credentials/.
const (
	routeCreateVault       = "POST /v1/vaults"
	routeListVaults        = "GET /v1/vaults"
	routeGetVault          = "GET /v1/vaults/{vault_id}"
	routeUpdateVault       = "POST /v1/vaults/{vault_id}"
	routeDeleteVault       = "DELETE /v1/vaults/{vault_id}"
	routeArchiveVault      = "POST /v1/vaults/{vault_id}/archive"
	routeCreateCredential  = "POST /v1/vaults/{vault_id}/credentials"
	routeGetCredential     = "GET /v1/vaults/{vault_id}/credentials/{credential_id}"
	routeUpdateCredential  = "POST /v1/vaults/{vault_id}/credentials/{credential_id}"
	routeDeleteCredential  = "DELETE /v1/vaults/{vault_id}/credentials/{credential_id}"
	routeArchiveCredential = "POST /v1/vaults/{vault_id}/credentials/{credential_id}/archive"

	managedAgentsBeta = "managed-agents-2026-04-01"
)

var allVaultRoutes = []string{
	routeCreateVault, routeListVaults, routeGetVault, routeUpdateVault, routeDeleteVault, routeArchiveVault,
	routeCreateCredential, routeGetCredential, routeUpdateCredential,
	routeDeleteCredential, routeArchiveCredential,
}

// vaultsFake is the vaults API on two sdktest collections; a credential is
// found only under its vault. An unknown ID answers 404 not_found_error
// (https://platform.claude.com/docs/en/api/errors).
type vaultsFake struct {
	*sdktest.Server
	vaults      *sdktest.Collection
	credentials *sdktest.Collection
}

// newVaultsFake returns the fake; the vaults list answers pages of at most
// pageSize objects (0: the request's limit alone) and leaves archived vaults
// out unless include_archived=true ("Defaults to false").
func newVaultsFake(t *testing.T, pageSize int) *vaultsFake {
	t.Helper()
	srv := sdktest.NewServer(t)
	f := &vaultsFake{Server: srv, vaults: srv.NewCollection("vlt_"), credentials: srv.NewCollection("vcrd_")}
	unlessArchived := func(r *http.Request, o map[string]any) bool {
		return r.URL.Query().Get("include_archived") == "true" || o["archived_at"] == nil
	}
	f.Handle(routeCreateVault, f.createVault)
	f.vaults.HandleGet(routeGetVault)
	f.Handle(routeUpdateVault, f.update(f.vaults, "vault_id", nil))
	f.Handle(routeDeleteVault, f.deleteVault)
	f.vaults.HandleArchive(routeArchiveVault)
	f.vaults.HandleList(routeListVaults, sdktest.PageToken, pageSize, unlessArchived)
	f.Handle(routeCreateCredential, f.createCredential)
	f.credentials.HandleGet(routeGetCredential)
	f.Handle(routeUpdateCredential, f.update(f.credentials, "credential_id", mergeAuth))
	f.credentials.HandleDelete(routeDeleteCredential, "vault_credential_deleted")
	f.credentials.HandleArchive(routeArchiveCredential)
	return f
}

func factories(f *vaultsFake) map[string]func() (tfprotov6.ProviderServer, error) {
	return providertest.ProviderFactories(f.Server)
}

func config(blocks string) string {
	return providertest.ProviderConfig(sdktest.APIKey) + blocks
}

// createVault stores a vault; metadata defaults to {} (always returned).
func (f *vaultsFake) createVault(w http.ResponseWriter, r *http.Request) {
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	obj := map[string]any{"type": "vault", "metadata": map[string]any{}, "archived_at": nil}
	maps.Copy(obj, body)
	sdktest.WriteJSON(w, http.StatusOK, f.vaults.Create(obj))
}

// deleteVault deletes a vault with its credentials (a deleted vault takes
// its credentials with it, internal/services/vaults/CLAUDE.md): the
// credential's Read then meets the 404 it treats as gone.
func (f *vaultsFake) deleteVault(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("vault_id")
	if !f.vaults.Delete(id) {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "vault "+id+" not found")
		return
	}
	for _, c := range f.credentials.List(func(o map[string]any) bool { return o["vault_id"] == id }) {
		f.credentials.Delete(c["id"].(string))
	}
	sdktest.WriteJSON(w, http.StatusOK, map[string]any{"id": id, "type": "vault_deleted"})
}

// update follows the vaults and credentials update references: display_name
// is replaced (null clears it), metadata is a patch where null deletes a key,
// and merge, when set, applies the auth member.
func (f *vaultsFake) update(c *sdktest.Collection, idParam string, merge func(stored, body map[string]any) map[string]any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue(idParam)
		stored, found := c.Get(id)
		if !found || (idParam == "credential_id" && stored["vault_id"] != r.PathValue("vault_id")) {
			sdktest.WriteAPIError(w, http.StatusNotFound, "", fmt.Sprintf("%s %s not found", idParam, id))
			return
		}
		body, ok := f.DecodeBody(w, r)
		if !ok {
			return
		}
		if auth, ok := body["auth"].(map[string]any); ok && merge != nil {
			body["auth"] = merge(stored["auth"].(map[string]any), auth)
		}
		metadata, patch := body["metadata"]
		delete(body, "metadata")
		obj, _ := c.Update(id, body, sdktest.Replace)
		if patch {
			obj, _ = c.Update(id, map[string]any{"metadata": metadata}, sdktest.Patch)
		}
		sdktest.WriteJSON(w, http.StatusOK, obj)
	}
}

// createCredential stores a credential under its vault, with the auth the
// response shows ("Sensitive fields are never returned in responses").
func (f *vaultsFake) createCredential(w http.ResponseWriter, r *http.Request) {
	vaultID := r.PathValue("vault_id")
	if !f.vaults.Has(vaultID) {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "vault "+vaultID+" not found")
		return
	}
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	obj := map[string]any{
		"type": "vault_credential", "vault_id": vaultID, "display_name": nil, "metadata": map[string]any{}, "archived_at": nil,
	}
	maps.Copy(obj, body)
	obj["auth"] = mergeAuth(map[string]any{}, body["auth"].(map[string]any))
	sdktest.WriteJSON(w, http.StatusOK, f.credentials.Create(obj))
}

// mergeAuth applies a create or update auth body to the stored auth and
// returns the response shape: the secrets (token, access_token,
// refresh_token, client_secret, secret_value) are dropped; on update an
// omitted field is kept, a refresh scope of "" clears it ("Send an empty
// string to clear the stored scope", which clearRemovedScope sends),
// token_endpoint_auth keeps its stored type ("It keeps its current
// token_endpoint_auth type", which buildTokenEndpointAuthUpdate relies on),
// networking is replaced as a whole, injection_location merges per field;
// on create an environment variable without injection_location gets both
// locations (the schema's documented default, which
// mapInjectionLocationResponseToObject maps back). References:
// https://platform.claude.com/docs/en/api/beta/vaults/credentials/create and
// .../credentials/update.
func mergeAuth(stored, body map[string]any) map[string]any {
	auth := map[string]any{}
	maps.Copy(auth, stored)
	for _, k := range []string{"type", "mcp_server_url", "expires_at", "secret_name", "networking"} {
		if v, ok := body[k]; ok {
			auth[k] = v
		}
	}
	// The API stores a bare host with a trailing slash (observed live), which
	// mcpServerURLForState maps back to the configured URL.
	if u, err := url.Parse(fmt.Sprint(auth["mcp_server_url"])); err == nil && auth["mcp_server_url"] != nil && u.Path == "" {
		u.Path = "/"
		auth["mcp_server_url"] = u.String()
	}
	if refresh, ok := body["refresh"].(map[string]any); ok {
		auth["refresh"] = mergeRefresh(stored["refresh"], refresh)
	}
	if auth["type"] == "environment_variable" {
		location, _ := auth["injection_location"].(map[string]any)
		if location == nil {
			location = map[string]any{"header": true, "body": true}
		}
		if update, ok := body["injection_location"].(map[string]any); ok {
			location = map[string]any{"header": location["header"], "body": location["body"]}
			maps.Copy(location, update)
		}
		auth["injection_location"] = location
	}
	return auth
}

func mergeRefresh(stored any, body map[string]any) map[string]any {
	refresh := map[string]any{}
	if s, ok := stored.(map[string]any); ok {
		maps.Copy(refresh, s)
	}
	for _, k := range []string{"client_id", "token_endpoint", "resource", "scope"} {
		if v, ok := body[k]; ok {
			refresh[k] = v
		}
	}
	if refresh["scope"] == "" {
		delete(refresh, "scope")
	}
	if tea, ok := body["token_endpoint_auth"].(map[string]any); ok && refresh["token_endpoint_auth"] == nil {
		refresh["token_endpoint_auth"] = map[string]any{"type": tea["type"]}
	}
	return refresh
}

// assertRequestShape checks the managed-agents beta and the standard key on
// every route called.
func (f *vaultsFake) assertRequestShape() {
	f.AssertShape(sdktest.Shape{Credential: sdktest.APIKey, Beta: managedAgentsBeta}, allVaultRoutes...)
}
