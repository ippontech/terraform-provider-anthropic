// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package apikeys_test

import (
	"fmt"
	"maps"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// Routes of the Admin API keys endpoints (Admin key, no beta header).
// References: https://platform.claude.com/docs/en/api/organization/api_keys/list,
// .../api_keys/retrieve and .../api_keys/update.
const (
	routeListKeys  = "GET /v1/organizations/api_keys"
	routeGetKey    = "GET /v1/organizations/api_keys/{api_key_id}"
	routeUpdateKey = "POST /v1/organizations/api_keys/{api_key_id}"
)

// keysFake serves the organization's API keys. There is no create endpoint:
// keys are seeded with Put, as the Console would create them.
type keysFake struct {
	*sdktest.Server
	keys *sdktest.Collection
}

func newKeysFake(t *testing.T, pageSize int) *keysFake {
	t.Helper()
	srv := sdktest.NewServer(t)
	f := &keysFake{Server: srv, keys: srv.NewCollection("apikey_")}
	// List API Keys: after_id pages; status, workspace_id and
	// created_by_user_id filters.
	f.keys.HandleList(routeListKeys, sdktest.AfterID, pageSize, func(r *http.Request, k map[string]any) bool {
		q := r.URL.Query()
		if s := q.Get("status"); s != "" && k["status"] != s {
			return false
		}
		if ws := q.Get("workspace_id"); ws != "" && scopeWorkspace(k) != ws {
			return false
		}
		if u := q.Get("created_by_user_id"); u != "" {
			by, _ := k["created_by"].(map[string]any)
			return by != nil && by["type"] == "user" && by["id"] == u
		}
		return true
	})
	f.keys.HandleGet(routeGetKey)
	f.Handle(routeUpdateKey, f.update)
	return f
}

func scopeWorkspace(k map[string]any) string {
	scope, _ := k["scope"].(map[string]any)
	ws, _ := scope["workspace_id"].(string)
	return ws
}

// update applies Update API Key: the name and status sent replace the stored
// ones, fields the body omits are kept; the answer is the APIKey object. An
// unknown key is a 404.
func (f *keysFake) update(w http.ResponseWriter, r *http.Request) {
	k, ok := f.keys.Get(r.PathValue("api_key_id"))
	if !ok {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "api key not found")
		return
	}
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	maps.Copy(k, body)
	f.keys.Put(k)
	sdktest.WriteJSON(w, http.StatusOK, k)
}

func keyID(n int) string { return fmt.Sprintf("apikey_%024d", n) }

// Shapes of an APIKey object, after the reference's scope and principal
// unions. workspace_id is the field of anthropic-version 2023-06-01, which
// internal/admin pins: null for an organization-scoped key and for a key in
// the Default Workspace (the reference, at 2026-10-21, no longer has it).
const (
	// workspaceUserKey: workspace scope, user principal, an expiry.
	workspaceUserKey = iota
	// orgServiceAccountKey: organization scope (no workspace), service
	// account principal, no expiry.
	orgServiceAccountKey
	// legacyDefaultKey: Default Workspace (workspace_id null, scope with the
	// real ID), no principal, no creator, no hint.
	legacyDefaultKey
)

// keyJSON is an APIKey object of the given shape.
func keyJSON(n, shape int, name, status string) map[string]any {
	k := map[string]any{
		"id":               keyID(n),
		"name":             name,
		"status":           status,
		"created_at":       fmt.Sprintf("2026-03-%02dT08:00:00Z", n),
		"partial_key_hint": fmt.Sprintf("sk-ant-api03-K%d...AAAA", n),
		"type":             "api_key",
	}
	switch shape {
	case workspaceUserKey:
		k["workspace_id"] = "wrkspc_team"
		k["scope"] = map[string]any{"type": "workspace", "workspace_id": "wrkspc_team"}
		k["principal"] = map[string]any{"type": "user_actor", "user_id": "user_alice"}
		k["expires_at"] = fmt.Sprintf("2027-03-%02dT08:00:00Z", n)
		k["created_by"] = map[string]any{"id": "user_alice", "type": "user"}
	case orgServiceAccountKey:
		k["workspace_id"] = nil
		k["scope"] = map[string]any{"type": "organization"}
		k["principal"] = map[string]any{"type": "service_account_actor", "service_account_id": "svcacct_ci"}
		k["expires_at"] = nil
		k["created_by"] = map[string]any{"id": "svcacct_admin", "type": "service_account"}
	case legacyDefaultKey:
		k["workspace_id"] = nil
		k["scope"] = map[string]any{"type": "workspace", "workspace_id": "wrkspc_default"}
		k["principal"] = nil
		k["expires_at"] = nil
		k["created_by"] = nil
		k["partial_key_hint"] = nil
	}
	return k
}

// keyChecks is the exact state value of each attribute for keyJSON(n, shape,
// name, status), by attribute name.
func keyChecks(n, shape int, name, status string) map[string]knownvalue.Check {
	c := map[string]knownvalue.Check{
		"id":               knownvalue.StringExact(keyID(n)),
		"name":             knownvalue.StringExact(name),
		"status":           knownvalue.StringExact(status),
		"created_at":       knownvalue.StringExact(fmt.Sprintf("2026-03-%02dT08:00:00Z", n)),
		"partial_key_hint": knownvalue.StringExact(fmt.Sprintf("sk-ant-api03-K%d...AAAA", n)),
		"type":             knownvalue.StringExact("api_key"),
	}
	switch shape {
	case workspaceUserKey:
		c["workspace_id"] = knownvalue.StringExact("wrkspc_team")
		c["scope"] = knownvalue.ObjectExact(map[string]knownvalue.Check{
			"type": knownvalue.StringExact("workspace"), "workspace_id": knownvalue.StringExact("wrkspc_team"),
		})
		c["principal"] = knownvalue.ObjectExact(map[string]knownvalue.Check{
			"type": knownvalue.StringExact("user_actor"), "user_id": knownvalue.StringExact("user_alice"), "service_account_id": knownvalue.Null(),
		})
		c["expires_at"] = knownvalue.StringExact(fmt.Sprintf("2027-03-%02dT08:00:00Z", n))
		c["created_by"] = knownvalue.ObjectExact(map[string]knownvalue.Check{
			"id": knownvalue.StringExact("user_alice"), "type": knownvalue.StringExact("user"),
		})
	case orgServiceAccountKey:
		c["workspace_id"] = knownvalue.Null()
		c["scope"] = knownvalue.ObjectExact(map[string]knownvalue.Check{
			"type": knownvalue.StringExact("organization"), "workspace_id": knownvalue.Null(),
		})
		c["principal"] = knownvalue.ObjectExact(map[string]knownvalue.Check{
			"type": knownvalue.StringExact("service_account_actor"), "user_id": knownvalue.Null(), "service_account_id": knownvalue.StringExact("svcacct_ci"),
		})
		c["expires_at"] = knownvalue.Null()
		c["created_by"] = knownvalue.ObjectExact(map[string]knownvalue.Check{
			"id": knownvalue.StringExact("svcacct_admin"), "type": knownvalue.StringExact("service_account"),
		})
	case legacyDefaultKey:
		c["workspace_id"] = knownvalue.Null()
		c["scope"] = knownvalue.ObjectExact(map[string]knownvalue.Check{
			"type": knownvalue.StringExact("workspace"), "workspace_id": knownvalue.StringExact("wrkspc_default"),
		})
		c["principal"] = knownvalue.Null()
		c["expires_at"] = knownvalue.Null()
		// No recorded creator still reads as an object, its id and type null
		// (apiKeyCreatedByValue): created_by has always been an object.
		c["created_by"] = knownvalue.ObjectExact(map[string]knownvalue.Check{
			"id": knownvalue.Null(), "type": knownvalue.Null(),
		})
		c["partial_key_hint"] = knownvalue.Null()
	}
	return c
}

// assertAdminRequests checks that every request the fake received carried
// the Admin key alone and no anthropic-beta header (the endpoints are GA).
func (f *keysFake) assertAdminRequests(t *testing.T) {
	t.Helper()
	f.AssertShape(sdktest.Shape{Credential: sdktest.AdminKey})
}

// expectKeyAction checks the planned action on the API key resource, then
// an empty plan after the apply and refresh.
func expectKeyAction(action plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return providertest.ExpectAction(keyAddr, action)
}
