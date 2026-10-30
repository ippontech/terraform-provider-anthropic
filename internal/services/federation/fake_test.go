// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation_test

import (
	"encoding/json"
	"maps"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// Routes of the Workload Identity Federation endpoints (OAuth org:admin
// bearer, no beta header, ?beta=true from the SDK). References:
// https://platform.claude.com/docs/en/api/beta/organization/federation/issuers/create,
// .../retrieve, .../update, .../archive, .../list, the same pages under
// .../federation/rules/, and .../federation/rules/workspaces/add, .../remove
// and .../list.
const (
	routeCreateIssuer    = "POST /v1/organizations/federation_issuers"
	routeListIssuers     = "GET /v1/organizations/federation_issuers"
	routeGetIssuer       = "GET /v1/organizations/federation_issuers/{issuer_id}"
	routeUpdateIssuer    = "POST /v1/organizations/federation_issuers/{issuer_id}"
	routeArchiveIssuer   = "POST /v1/organizations/federation_issuers/{issuer_id}/archive"
	routeCreateRule      = "POST /v1/organizations/federation_rules"
	routeListRules       = "GET /v1/organizations/federation_rules"
	routeGetRule         = "GET /v1/organizations/federation_rules/{rule_id}"
	routeUpdateRule      = "POST /v1/organizations/federation_rules/{rule_id}"
	routeArchiveRule     = "POST /v1/organizations/federation_rules/{rule_id}/archive"
	routeAddWorkspace    = "POST /v1/organizations/federation_rules/{federation_rule_id}/workspaces"
	routeListWorkspaces  = "GET /v1/organizations/federation_rules/{federation_rule_id}/workspaces"
	routeRemoveWorkspace = "DELETE /v1/organizations/federation_rules/{federation_rule_id}/workspaces/{workspace_id}"
)

var allFederationRoutes = []string{
	routeCreateIssuer, routeListIssuers, routeGetIssuer, routeUpdateIssuer, routeArchiveIssuer,
	routeCreateRule, routeListRules, routeGetRule, routeUpdateRule, routeArchiveRule,
	routeAddWorkspace, routeListWorkspaces, routeRemoveWorkspace,
}

// fakeServiceAccountNames and fakeWorkspaceNames are the names the API
// resolves target and workspace IDs to.
var (
	fakeServiceAccountNames = map[string]string{"svac_001": "sa-one", "svac_002": "sa-two"}
	fakeWorkspaceNames      = map[string]string{"wrkspc_a": "alpha", "wrkspc_b": "beta"}
)

// federationFake is the federation API on three sdktest collections. An
// unknown ID answers 404 not_found_error
// (https://platform.claude.com/docs/en/api/errors). Two response quirks the
// resources work around are reproduced: the rule create response has an empty
// issuer_name (#243), the rule update response no
// target.service_account_name (#349, "optional string or null" in the update
// reference); and the workspace add response has a null workspace_name, which
// only the list fills in (federation_rule_workspace_resource.go).
type federationFake struct {
	*sdktest.Server
	issuers, rules, workspaces *sdktest.Collection
	// failReadAfterRuleUpdate makes the first GET of a rule after each
	// update answer 403, which ends the update's consistency wait at once.
	failReadAfterRuleUpdate atomic.Bool
}

// newFederationFake returns the fake; the list endpoints answer pages of at
// most pageSize objects (0: the request's limit alone).
func newFederationFake(t *testing.T, pageSize int) *federationFake {
	t.Helper()
	srv := sdktest.NewServer(t)
	f := &federationFake{Server: srv, issuers: srv.NewCollection("fdis_"), rules: srv.NewCollection("fdrl_"), workspaces: srv.NewCollection("")}
	// "include_archived: Include archived resources. Defaults to false."
	live := func(r *http.Request, o map[string]any) bool {
		return r.URL.Query().Get("include_archived") == "true" || o["archived_at"] == nil
	}
	f.Handle(routeCreateIssuer, f.write(f.issuers, "", f.applyIssuer))
	f.issuers.HandleGet(routeGetIssuer)
	f.Handle(routeUpdateIssuer, f.write(f.issuers, "issuer_id", f.applyIssuer))
	f.issuers.HandleArchive(routeArchiveIssuer)
	f.issuers.HandleList(routeListIssuers, sdktest.PageToken, pageSize, live)
	f.Handle(routeCreateRule, f.write(f.rules, "", f.applyRule))
	f.rules.HandleGet(routeGetRule)
	f.Handle(routeUpdateRule, f.write(f.rules, "rule_id", f.applyRule))
	f.rules.HandleArchive(routeArchiveRule)
	f.rules.HandleList(routeListRules, sdktest.PageToken, pageSize, func(r *http.Request, o map[string]any) bool {
		return live(r, o) && (!r.URL.Query().Has("issuer_id") || o["issuer_id"] == r.URL.Query().Get("issuer_id"))
	})
	f.Handle(routeAddWorkspace, f.ruleMustExist(f.addWorkspace))
	f.Handle(routeListWorkspaces, f.ruleMustExist(func(w http.ResponseWriter, r *http.Request) {
		items := f.workspaces.List(func(o map[string]any) bool { return o["federation_rule_id"] == r.PathValue("federation_rule_id") })
		f.Paginate(w, r, sdktest.PageToken, pageSize, items)
	}))
	// "Idempotent; succeeds even if the enablement was already removed."
	f.Handle(routeRemoveWorkspace, f.ruleMustExist(func(w http.ResponseWriter, r *http.Request) {
		ruleID, workspaceID := r.PathValue("federation_rule_id"), r.PathValue("workspace_id")
		f.workspaces.Delete(ruleID + ":" + workspaceID)
		sdktest.WriteJSON(w, http.StatusOK, map[string]any{"type": "federation_rule_workspace_deleted", "federation_rule_id": ruleID, "workspace_id": workspaceID})
	}))
	return f
}

func factories(f *federationFake) map[string]func() (tfprotov6.ProviderServer, error) {
	return providertest.ProviderFactories(f.Server)
}

func config(blocks string) string {
	return providertest.ProviderConfig(sdktest.AuthToken) + blocks
}

// write handles a create (idParam "") or an update of c: the body goes
// through apply, which returns the response; the stored object keeps the
// fields the update omits.
func (f *federationFake) write(c *sdktest.Collection, idParam string, apply func(stored, body map[string]any, update bool) map[string]any) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			sdktest.WriteAPIError(w, http.StatusBadRequest, "", err.Error())
			return
		}
		if attrs, _ := body["attributes"].(map[string]any); idParam == "" && len(attrs) > 0 {
			sdktest.WriteAPIError(w, http.StatusBadRequest, "", "attributes: not yet supported")
			return
		}
		if idParam == "" {
			stored := c.Create(map[string]any{})
			sdktest.WriteJSON(w, http.StatusOK, apply(stored, body, false))
			return
		}
		stored, ok := c.Get(r.PathValue(idParam))
		if !ok {
			sdktest.WriteAPIError(w, http.StatusNotFound, "", "not found")
			return
		}
		// rules: "attributes ... Not yet supported; any non-empty value is
		// rejected with 400" (update reference; the create one says the same).
		if attrs, _ := body["attributes"].(map[string]any); len(attrs) > 0 {
			sdktest.WriteAPIError(w, http.StatusBadRequest, "", "attributes: not yet supported")
			return
		}
		updated, _ := c.Update(stored["id"].(string), map[string]any{}, sdktest.Replace)
		sdktest.WriteJSON(w, http.StatusOK, apply(updated, body, true))
	}
}

// applyIssuer stores an issuer with the create defaults of the reference
// (check_jti true, max_jwt_lifetime_seconds 3600, OIDC discovery), and a poll
// status except for inline keys ("Typically null for inline-key issuers").
// An update replaces each field it carries, jwks as a whole;
// jwks_polling_disabled = false clears jwks_polling_disabled_at.
func (f *federationFake) applyIssuer(stored, body map[string]any, update bool) map[string]any {
	if !update {
		maps.Copy(stored, map[string]any{
			"type": "federation_issuer", "check_jti": true, "max_jwt_lifetime_seconds": 3600,
			"jwks": map[string]any{"type": "discovery"}, "jwks_polling_disabled_at": nil,
			"created_by_actor_id": "user_001", "archived_at": nil, "archived_by_actor_id": nil,
		})
	}
	for _, k := range []string{"name", "issuer_url", "check_jti", "max_jwt_lifetime_seconds", "jwks"} {
		if v, ok := body[k]; ok && v != nil {
			stored[k] = v
		}
	}
	if body["jwks_polling_disabled"] == false {
		stored["jwks_polling_disabled_at"] = nil
	}
	stored["poll_status"] = map[string]any{"consecutive_failures": 0, "last_fetched_at": nil, "next_poll_at": "2026-02-01T00:00:00Z"}
	if stored["jwks"].(map[string]any)["type"] == "inline" {
		stored["poll_status"] = nil
	}
	stored["updated_by_actor_id"] = "user_001"
	f.issuers.Put(stored)
	return maps.Clone(stored)
}

// applyRule stores a rule: each writable field the body carries replaces the
// stored one, match and target as whole objects, a null description clears
// it; attributes stay null; target resolves its service account name and
// workspace_ids lists workspace_id.
func (f *federationFake) applyRule(stored, body map[string]any, update bool) map[string]any {
	if !update {
		maps.Copy(stored, map[string]any{
			"type": "federation_rule", "issuer_id": body["issuer_id"], "issuer_name": "github", "description": nil,
			"workspace_id": nil, "applies_to_all_workspaces": false, "token_lifetime_seconds": 3600, "attributes": nil,
			"created_by_actor_id": "user_001", "archived_at": nil, "archived_by_actor_id": nil,
		})
		if issuer, ok := f.issuers.Get(stored["issuer_id"].(string)); ok {
			stored["issuer_name"] = issuer["name"]
		}
	}
	for _, k := range []string{"name", "description", "match", "oauth_scope", "workspace_id", "applies_to_all_workspaces", "token_lifetime_seconds"} {
		if v, ok := body[k]; ok {
			stored[k] = v
		}
	}
	stored["attributes"] = nil // "Not yet supported; always null." (retrieve reference)
	if target, ok := body["target"].(map[string]any); ok {
		id, _ := target["service_account_id"].(string)
		stored["target"] = map[string]any{"type": "service_account", "service_account_id": id, "service_account_name": fakeServiceAccountNames[id]}
	}
	stored["workspace_ids"] = []any{}
	if id, ok := stored["workspace_id"].(string); ok {
		stored["workspace_ids"] = []any{id}
	}
	stored["updated_by_actor_id"] = "user_001"
	f.rules.Put(stored)

	response := maps.Clone(stored)
	if update && f.failReadAfterRuleUpdate.Load() {
		f.FailNext(routeGetRule, http.StatusForbidden, 1)
	}
	if update {
		target := maps.Clone(stored["target"].(map[string]any))
		delete(target, "service_account_name") // #349
		response["target"] = target
	} else {
		response["issuer_name"] = "" // #243
	}
	return response
}

// addWorkspace enables a rule for a workspace.
func (f *federationFake) addWorkspace(w http.ResponseWriter, r *http.Request) {
	var body struct {
		WorkspaceID string `json:"workspace_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", err.Error())
		return
	}
	ruleID := r.PathValue("federation_rule_id")
	added := f.workspaces.Create(map[string]any{
		"id": ruleID + ":" + body.WorkspaceID, "type": "federation_rule_workspace", "federation_rule_id": ruleID,
		"workspace_id": body.WorkspaceID, "workspace_name": fakeWorkspaceNames[body.WorkspaceID], "created_by_actor_id": "user_001",
	})
	added["workspace_name"] = nil
	sdktest.WriteJSON(w, http.StatusOK, added)
}

// ruleMustExist answers 404 for the workspaces of an unknown rule.
func (f *federationFake) ruleMustExist(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !f.rules.Has(r.PathValue("federation_rule_id")) {
			sdktest.WriteAPIError(w, http.StatusNotFound, "", "Federation rule not found")
			return
		}
		h(w, r)
	}
}

// assertRequestShape checks the OAuth bearer and the absence of a beta header
// on every route called.
func (f *federationFake) assertRequestShape() {
	for _, route := range allFederationRoutes {
		if len(f.Requests(route)) > 0 {
			f.AssertNoBeta(route)
			f.AssertCredential(route, sdktest.AuthToken)
		}
	}
}
