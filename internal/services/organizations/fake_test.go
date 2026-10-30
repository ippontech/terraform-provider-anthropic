// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package organizations_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"

	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// Routes of the Admin API organization endpoints (Admin key, no beta header;
// the invite routes take ?beta=true in the query). References:
// https://platform.claude.com/docs/en/api/organization/retrieve,
// .../organization/users/{list,retrieve,update,remove} and
// .../organization/invites/{create,list,retrieve,delete}.
const (
	routeGetOrganization = "GET /v1/organizations/me"
	routeListUsers       = "GET /v1/organizations/users"
	routeGetUser         = "GET /v1/organizations/users/{user_id}"
	routeUpdateUser      = "POST /v1/organizations/users/{user_id}"
	routeDeleteUser      = "DELETE /v1/organizations/users/{user_id}"
	routeCreateInvite    = "POST /v1/organizations/invites"
	routeListInvites     = "GET /v1/organizations/invites"
	routeGetInvite       = "GET /v1/organizations/invites/{invite_id}"
	routeDeleteInvite    = "DELETE /v1/organizations/invites/{invite_id}"
)

// orgFake serves one organization, its users and its invites.
type orgFake struct {
	*sdktest.Server
	users   *sdktest.Collection
	invites *sdktest.Collection
	created atomic.Int64
}

func newOrgFake(t *testing.T, pageSize int) *orgFake {
	t.Helper()
	srv := sdktest.NewServer(t)
	f := &orgFake{Server: srv, users: srv.NewCollection("user_"), invites: srv.NewCollection("invite_")}

	// "OrganizationInfo object": type, id (uuid), name.
	f.Respond(routeGetOrganization, http.StatusOK, map[string]any{
		"id": "12345678-1234-5678-1234-567812345678", "name": "Ippon Test Org", "type": "organization",
	})

	// List Users: after_id pages, "email: Filter by user email".
	f.users.HandleList(routeListUsers, sdktest.AfterID, pageSize, func(r *http.Request, u map[string]any) bool {
		email := r.URL.Query().Get("email")
		return email == "" || u["email"] == email
	})
	f.users.HandleGet(routeGetUser)
	f.Handle(routeUpdateUser, f.updateUser)
	// Remove User returns {"id", "type": "user_deleted"}.
	f.users.HandleDelete(routeDeleteUser, "user_deleted")

	f.Handle(routeCreateInvite, f.createInvite)
	// List Invites: after_id pages; "email" filter; "statuses: Repeatable;
	// values are OR'ed together. Omit to return pending, accepted, and
	// expired Invites alike".
	f.invites.HandleList(routeListInvites, sdktest.AfterID, pageSize, func(r *http.Request, inv map[string]any) bool {
		q := r.URL.Query()
		if email := q.Get("email"); email != "" && inv["email"] != email {
			return false
		}
		statuses := q["statuses"]
		if len(statuses) == 0 {
			return inv["status"] != "deleted"
		}
		return slices.Contains(statuses, fmt.Sprint(inv["status"]))
	})
	f.invites.HandleGet(routeGetInvite)
	// Delete Invite returns {"id", "type": "invite_deleted"}.
	f.invites.HandleDelete(routeDeleteInvite, "invite_deleted")
	return f
}

// userJSON is an OrganizationUser as List/Get/Update User return it.
func userJSON(n int, role string) map[string]any {
	return map[string]any{
		"id":       userID(n),
		"email":    fmt.Sprintf("user%d@example.com", n),
		"name":     fmt.Sprintf("User %d", n),
		"role":     role,
		"added_at": fmt.Sprintf("2026-01-%02dT10:00:00Z", n),
		"type":     "user",
	}
}

func userID(n int) string { return fmt.Sprintf("user_%024d", n) }

// updateUser applies Update User: the body carries only role, the answer is
// the updated OrganizationUser; an unknown user is a 404.
func (f *orgFake) updateUser(w http.ResponseWriter, r *http.Request) {
	u, ok := f.users.Get(r.PathValue("user_id"))
	if !ok {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "user not found")
		return
	}
	var body struct {
		Role string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Role == "" {
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", "role: field required")
		return
	}
	u["role"] = body.Role
	f.users.Put(u)
	sdktest.WriteJSON(w, http.StatusOK, u)
}

// inviteJSON is an OrganizationInvite: accepted_at null until accepted,
// rbac_group_ids "[] when none".
func inviteJSON(n int, email, role, status string) map[string]any {
	return map[string]any{
		"id":             inviteID(n),
		"email":          email,
		"role":           role,
		"status":         status,
		"invited_at":     fmt.Sprintf("2026-02-%02dT09:00:00Z", n),
		"expires_at":     fmt.Sprintf("2026-03-%02dT09:00:00Z", n),
		"accepted_at":    nil,
		"rbac_group_ids": []string{},
		"type":           "invite",
	}
}

func inviteID(n int) string { return fmt.Sprintf("invite_%024d", n) }

// createInvite applies Create Invite: a pending invite for the body's email
// and role.
func (f *orgFake) createInvite(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Email string `json:"email"`
		Role  string `json:"role"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Email == "" || body.Role == "" {
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", "email and role are required")
		return
	}
	inv := inviteJSON(int(f.created.Add(1)), body.Email, body.Role, "pending")
	f.invites.Put(inv)
	sdktest.WriteJSON(w, http.StatusOK, inv)
}

// setInviteStatus changes an invite's status behind Terraform's back.
func (f *orgFake) setInviteStatus(t *testing.T, id, status string) {
	t.Helper()
	inv, ok := f.invites.Get(id)
	if !ok {
		t.Fatalf("invite %s not in the fake", id)
	}
	inv["status"] = status
	if status == "accepted" {
		inv["accepted_at"] = "2026-02-20T12:00:00Z"
	}
	f.invites.Put(inv)
}

// assertAdminRequests checks that every request the fake received carried
// the Admin key alone and no anthropic-beta header (the Admin endpoints are
// GA; the invites take ?beta=true in the query instead).
func (f *orgFake) assertAdminRequests(t *testing.T) {
	t.Helper()
	routes := map[string]bool{}
	for _, r := range f.Requests("") {
		routes[r.Route] = true
	}
	if len(routes) == 0 {
		t.Fatal("the fake received no request")
	}
	for route := range routes {
		f.AssertCredential(route, sdktest.AdminKey)
		f.AssertNoBeta(route)
	}
}

func expectAction(addr string, action plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{
		PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, action)},
		PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
	}
}
