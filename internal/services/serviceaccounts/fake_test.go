// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package serviceaccounts_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"

	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// Routes of the WIF service account endpoints (beta, ?beta=true, OAuth
// org:admin bearer; no anthropic-beta header). References:
// https://platform.claude.com/docs/en/api/beta/organization/service_accounts/create,
// .../retrieve, .../update, .../list and .../archive, the same pages under
// .../service_accounts/workspaces/ (list, add, remove), and
// https://platform.claude.com/docs/en/api/beta/organization/workspaces/service_accounts/update.
const (
	routeCreateServiceAccount  = "POST /v1/organizations/service_accounts"
	routeListServiceAccounts   = "GET /v1/organizations/service_accounts"
	routeGetServiceAccount     = "GET /v1/organizations/service_accounts/{service_account_id}"
	routeUpdateServiceAccount  = "POST /v1/organizations/service_accounts/{service_account_id}"
	routeArchiveServiceAccount = "POST /v1/organizations/service_accounts/{service_account_id}/archive"
	routeListMemberships       = "GET /v1/organizations/service_accounts/{service_account_id}/workspaces"
	routeAddMembership         = "POST /v1/organizations/service_accounts/{service_account_id}/workspaces"
	routeRemoveMembership      = "DELETE /v1/organizations/service_accounts/{service_account_id}/workspaces/{workspace_id}"
	routeUpdateMembership      = "POST /v1/organizations/workspaces/{workspace_id}/service_accounts/{service_account_id}"

	// defaultWorkspaceID is the organization's default workspace, where every
	// service account has an implicit membership.
	defaultWorkspaceID = "wrkspc_default"
	// fakeActorID is the actor the fake records on every write: the caller
	// behind the dummy bearer.
	fakeActorID = "user_01SdktestAdmin"
)

var allRoutes = []string{
	routeCreateServiceAccount, routeListServiceAccounts, routeGetServiceAccount,
	routeUpdateServiceAccount, routeArchiveServiceAccount, routeListMemberships,
	routeAddMembership, routeRemoveMembership, routeUpdateMembership,
}

// serviceAccountsFake is the service account API on two sdktest
// collections. Memberships carry an "id" (service_account_id:workspace_id)
// the API does not send, because the fake's pager keys its cursors by it;
// the SDK ignores the extra field, and Add and Update answer without it.
type serviceAccountsFake struct {
	*sdktest.Server
	accounts    *sdktest.Collection
	memberships *sdktest.Collection
}

func newServiceAccountsFake(t *testing.T, pageSize int) *serviceAccountsFake {
	t.Helper()
	srv := sdktest.NewServer(t)
	f := &serviceAccountsFake{Server: srv, accounts: srv.NewCollection("svac_"), memberships: srv.NewCollection("")}
	f.Handle(routeCreateServiceAccount, f.create)
	f.Handle(routeListServiceAccounts, func(w http.ResponseWriter, r *http.Request) { f.list(w, r, pageSize) })
	f.accounts.HandleGet(routeGetServiceAccount)
	f.Handle(routeUpdateServiceAccount, f.update)
	f.Handle(routeArchiveServiceAccount, f.archive)
	f.Handle(routeListMemberships, func(w http.ResponseWriter, r *http.Request) { f.listMemberships(w, r, pageSize) })
	f.Handle(routeAddMembership, f.addMembership)
	f.Handle(routeRemoveMembership, f.removeMembership)
	f.Handle(routeUpdateMembership, f.updateMembership)
	return f
}

// newAccount stores a live service account as the create reference
// describes it and returns it: description null unless given, role
// "developer" unless given.
func (f *serviceAccountsFake) newAccount(name string, description, role any) map[string]any {
	if role == nil {
		role = "developer"
	}
	return f.accounts.Create(map[string]any{
		"type":                 "service_account",
		"name":                 name,
		"description":          description,
		"organization_role":    role,
		"created_by_actor_id":  fakeActorID,
		"updated_by_actor_id":  fakeActorID,
		"archived_at":          nil,
		"archived_by_actor_id": nil,
	})
}

// create: "a duplicate name returns 409" (create reference, name).
func (f *serviceAccountsFake) create(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeBody(w, r)
	if !ok {
		return
	}
	name, _ := body["name"].(string)
	if len(f.accounts.List(func(a map[string]any) bool { return a["name"] == name })) > 0 {
		sdktest.WriteAPIError(w, http.StatusConflict, "", "service account name "+name+" is already taken")
		return
	}
	sdktest.WriteJSON(w, http.StatusOK, f.newAccount(name, body["description"], body["organization_role"]))
}

// list is newest first, archived accounts only with include_archived=true
// (list reference).
func (f *serviceAccountsFake) list(w http.ResponseWriter, r *http.Request, pageSize int) {
	withArchived := r.URL.Query().Get("include_archived") == "true"
	items := f.accounts.List(func(a map[string]any) bool { return withArchived || a["archived_at"] == nil })
	slices.Reverse(items)
	f.Paginate(w, r, sdktest.PageToken, pageSize, items)
}

// update follows the update reference: an archived account is a 400; an
// omitted field is kept; a null description clears it ("the field is stored
// as an empty string"); a null organization_role leaves it unchanged.
func (f *serviceAccountsFake) update(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("service_account_id")
	account, ok := f.accounts.Get(id)
	if !ok {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "service account "+id+" not found")
		return
	}
	if account["archived_at"] != nil {
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", "archived service accounts cannot be updated")
		return
	}
	body, ok := decodeBody(w, r)
	if !ok {
		return
	}
	patch := map[string]any{"updated_by_actor_id": fakeActorID}
	if description, set := body["description"]; set {
		if description == nil {
			description = ""
		}
		patch["description"] = description
	}
	if role := body["organization_role"]; role != nil {
		patch["organization_role"] = role
	}
	account, _ = f.accounts.Update(id, patch, sdktest.Replace)
	sdktest.WriteJSON(w, http.StatusOK, account)
}

// archive is idempotent and keeps the original archived_at (archive
// reference).
func (f *serviceAccountsFake) archive(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("service_account_id")
	account, ok := f.accounts.Archive(id)
	if !ok {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "service account "+id+" not found")
		return
	}
	if account["archived_by_actor_id"] == nil {
		account["archived_by_actor_id"] = fakeActorID
		f.accounts.Put(account)
	}
	sdktest.WriteJSON(w, http.StatusOK, account)
}

// liveAccount answers the error the membership endpoints give for an
// account that is unknown (404) or archived (400: "Archived service accounts
// cannot be added and are rejected", add and update references).
func (f *serviceAccountsFake) liveAccount(w http.ResponseWriter, id string) bool {
	account, ok := f.accounts.Get(id)
	switch {
	case !ok:
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "service account "+id+" not found")
	case account["archived_at"] != nil:
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", "service account "+id+" is archived")
	}
	return ok && account["archived_at"] == nil
}

func membershipKey(serviceAccountID, workspaceID string) string {
	return serviceAccountID + ":" + workspaceID
}

// addMembership stores an explicit membership; an existing one gets the new
// role (add reference).
func (f *serviceAccountsFake) addMembership(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("service_account_id")
	if !f.liveAccount(w, id) {
		return
	}
	body, ok := decodeBody(w, r)
	if !ok {
		return
	}
	workspaceID, _ := body["workspace_id"].(string)
	m := map[string]any{
		"id":                  membershipKey(id, workspaceID),
		"type":                "service_account_workspace_member",
		"service_account_id":  id,
		"workspace_id":        workspaceID,
		"workspace_role":      body["workspace_role"],
		"implicit":            false,
		"created_by_actor_id": fakeActorID,
	}
	f.memberships.Put(m)
	delete(m, "id")
	sdktest.WriteJSON(w, http.StatusOK, m)
}

// listMemberships follows the list reference: an unknown account is a 404;
// an archived one lists nothing; the implicit default-workspace membership
// comes first while the account has no explicit one there.
func (f *serviceAccountsFake) listMemberships(w http.ResponseWriter, r *http.Request, pageSize int) {
	id := r.PathValue("service_account_id")
	account, ok := f.accounts.Get(id)
	if !ok {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "service account "+id+" not found")
		return
	}
	var items []map[string]any
	if account["archived_at"] == nil {
		items = f.memberships.List(func(m map[string]any) bool { return m["service_account_id"] == id })
		if !f.memberships.Has(membershipKey(id, defaultWorkspaceID)) {
			items = append([]map[string]any{{
				"id":                  membershipKey(id, defaultWorkspaceID),
				"type":                "service_account_workspace_member",
				"service_account_id":  id,
				"workspace_id":        defaultWorkspaceID,
				"workspace_role":      "workspace_user",
				"implicit":            true,
				"created_by_actor_id": nil,
			}}, items...)
		}
	}
	f.Paginate(w, r, sdktest.PageToken, pageSize, items)
}

// updateMembership replaces the role of an explicit membership; only those
// can be updated (workspaces/service_accounts/update reference).
func (f *serviceAccountsFake) updateMembership(w http.ResponseWriter, r *http.Request) {
	id, workspaceID := r.PathValue("service_account_id"), r.PathValue("workspace_id")
	if !f.liveAccount(w, id) {
		return
	}
	m, ok := f.memberships.Get(membershipKey(id, workspaceID))
	if !ok {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "no explicit membership of "+id+" in "+workspaceID)
		return
	}
	body, ok := decodeBody(w, r)
	if !ok {
		return
	}
	m["workspace_role"] = body["workspace_role"]
	f.memberships.Put(m)
	delete(m, "id")
	sdktest.WriteJSON(w, http.StatusOK, m)
}

// removeMembership is idempotent: 200 even when the membership is already
// gone (remove reference).
func (f *serviceAccountsFake) removeMembership(w http.ResponseWriter, r *http.Request) {
	id, workspaceID := r.PathValue("service_account_id"), r.PathValue("workspace_id")
	f.memberships.Delete(membershipKey(id, workspaceID))
	sdktest.WriteJSON(w, http.StatusOK, map[string]any{
		"type":               "service_account_workspace_member_deleted",
		"service_account_id": id,
		"workspace_id":       workspaceID,
	})
}

// assertRequestShape checks what every request of a test must carry: the
// OAuth bearer, ?beta=true and no anthropic-beta header, on every route
// called.
func (f *serviceAccountsFake) assertRequestShape(t *testing.T) {
	t.Helper()
	for _, route := range allRoutes {
		if len(f.Requests(route)) == 0 {
			continue
		}
		f.AssertCredential(route, sdktest.AuthToken)
		f.AssertNoBeta(route)
		for _, r := range f.Requests(route) {
			if r.Query.Get("beta") != "true" {
				t.Errorf("%s %s without ?beta=true", r.Method, r.Path)
			}
		}
	}
}

// requestPaths returns the path and fault of every request to route.
func (f *serviceAccountsFake) requestPaths(route string) []string {
	var out []string
	for _, r := range f.Requests(route) {
		out = append(out, fmt.Sprintf("%s %s", r.Path, r.Fault))
	}
	return out
}

// decodeBody reads a JSON object body, numbers kept as json.Number, or
// answers 400 invalid_request_error.
func decodeBody(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	body := map[string]any{}
	dec := json.NewDecoder(r.Body)
	dec.UseNumber()
	if err := dec.Decode(&body); err != nil {
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", "body is not a JSON object: "+err.Error())
		return nil, false
	}
	return body, true
}
