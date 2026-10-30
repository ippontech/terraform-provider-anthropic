// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package rbac_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// Routes of the Claude Enterprise RBAC endpoints (Admin API, scoped Admin
// key, ?beta=true in the query, no beta header). References:
// https://platform.claude.com/docs/en/api/beta/organization/rbac_groups/{create,list,retrieve,update,delete}
// and .../rbac_groups/members/{add,list,remove}.
const (
	routeCreateGroup     = "POST /v1/organizations/rbac_groups"
	routeListGroups      = "GET /v1/organizations/rbac_groups"
	routeGetGroup        = "GET /v1/organizations/rbac_groups/{rbac_group_id}"
	routeUpdateGroup     = "POST /v1/organizations/rbac_groups/{rbac_group_id}"
	routeDeleteGroup     = "DELETE /v1/organizations/rbac_groups/{rbac_group_id}"
	routeAddGroupMember  = "POST /v1/organizations/rbac_groups/{rbac_group_id}/members"
	routeListGroupMember = "GET /v1/organizations/rbac_groups/{rbac_group_id}/members"
	routeDelGroupMember  = "DELETE /v1/organizations/rbac_groups/{rbac_group_id}/members/{user_id}"
)

// rbacFake serves the organization's RBAC groups and their members.
type rbacFake struct {
	*sdktest.Server
	pageSize int

	mu      sync.Mutex
	created int
	groups  []map[string]any
	members []map[string]any
}

func newRBACFake(t *testing.T, pageSize int) *rbacFake {
	t.Helper()
	f := &rbacFake{Server: sdktest.NewServer(t), pageSize: pageSize}
	f.Handle(routeCreateGroup, f.createGroup)
	f.Handle(routeListGroups, func(w http.ResponseWriter, r *http.Request) { f.page(w, r, f.snapshot(&f.groups, nil), "id") })
	f.Handle(routeGetGroup, f.getGroup)
	f.Handle(routeUpdateGroup, f.updateGroup)
	f.Handle(routeDeleteGroup, f.deleteGroup)
	f.Handle(routeAddGroupMember, f.addMember)
	f.Handle(routeListGroupMember, f.listMembers)
	f.Handle(routeDelGroupMember, f.removeMember)
	return f
}

func groupID(n int) string { return fmt.Sprintf("rbac_group_%024d", n) }

// groupJSON is a BetaRBACGroup under anthropic-version 2023-06-01: role_ids
// and its deprecated alias roles (internal/services/rbac, rbac.go).
func groupJSON(n int, name, source string, roleIDs ...string) map[string]any {
	if roleIDs == nil {
		roleIDs = []string{}
	}
	return map[string]any{
		"type": "rbac_group", "id": groupID(n), "name": name, "source_type": source,
		"role_ids": roleIDs, "roles": roleIDs,
		"created_at": fmt.Sprintf("2026-07-%02dT08:00:00Z", n), "updated_at": fmt.Sprintf("2026-07-%02dT08:00:00Z", n),
	}
}

// email is the address of a user of the fake organization.
func email(userID string) string { return userID + "@example.com" }

func (f *rbacFake) putGroup(g map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created++
	f.groups = append(f.groups, g)
}

func (f *rbacFake) putMember(groupID, userID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.members = append(f.members, memberJSON(groupID, userID))
}

// memberJSON is a BetaRBACGroupMember: rbac_group_id and its deprecated alias
// group_id under 2023-06-01.
func memberJSON(groupID, userID string) map[string]any {
	return map[string]any{
		"type": "rbac_group_member", "rbac_group_id": groupID, "group_id": groupID, "user_id": userID,
		"email": email(userID), "created_at": "2026-07-20T09:00:00Z",
	}
}

// snapshot copies the items of *list that keep accepts, under the lock.
func (f *rbacFake) snapshot(list *[]map[string]any, keep func(map[string]any) bool) []map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []map[string]any
	for _, it := range *list {
		if keep == nil || keep(it) {
			out = append(out, sdktest.DeepCopy(it))
		}
	}
	return out
}

// find returns the index of the item of list whose id is id, -1 when none.
// The caller holds f.mu.
func find(list []map[string]any, id string) int {
	return slices.IndexFunc(list, func(it map[string]any) bool { return it["id"] == id })
}

func (f *rbacFake) group(id string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i := find(f.groups, id); i >= 0 {
		return sdktest.DeepCopy(f.groups[i])
	}
	return nil
}

func (f *rbacFake) hasMember(groupID, userID string) bool {
	return len(f.snapshot(&f.members, func(m map[string]any) bool {
		return m["rbac_group_id"] == groupID && m["user_id"] == userID
	})) > 0
}

func (f *rbacFake) dropMember(groupID, userID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.members = slices.DeleteFunc(f.members, func(m map[string]any) bool {
		return m["rbac_group_id"] == groupID && m["user_id"] == userID
	})
}

func (f *rbacFake) dropGroup(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.groups = slices.DeleteFunc(f.groups, func(g map[string]any) bool { return g["id"] == id })
}

// createGroup applies Create RBAC Group: a direct group without role (role
// attachment "is managed in the admin settings and is read-only on this
// API").
func (f *rbacFake) createGroup(w http.ResponseWriter, r *http.Request) {
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created++
	g := groupJSON(f.created, fmt.Sprint(body["name"]), "direct")
	f.groups = append(f.groups, g)
	sdktest.WriteJSON(w, http.StatusOK, g)
}

func (f *rbacFake) getGroup(w http.ResponseWriter, r *http.Request) {
	if g := f.group(r.PathValue("rbac_group_id")); g != nil {
		sdktest.WriteJSON(w, http.StatusOK, g)
		return
	}
	sdktest.WriteAPIError(w, http.StatusNotFound, "", "rbac group not found")
}

// updateGroup applies Update RBAC Group (the name). The refusal to rename a
// SCIM group is canned by the test that needs it.
func (f *rbacFake) updateGroup(w http.ResponseWriter, r *http.Request) {
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	i := find(f.groups, r.PathValue("rbac_group_id"))
	if i < 0 {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "rbac group not found")
		return
	}
	f.groups[i]["name"] = body["name"]
	f.groups[i]["updated_at"] = "2026-08-01T00:00:00Z"
	sdktest.WriteJSON(w, http.StatusOK, f.groups[i])
}

// deleteGroup answers {"id", "type": "rbac_group_deleted"}.
func (f *rbacFake) deleteGroup(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := r.PathValue("rbac_group_id")
	if find(f.groups, id) < 0 {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "rbac group not found")
		return
	}
	f.groups = slices.DeleteFunc(f.groups, func(g map[string]any) bool { return g["id"] == id })
	sdktest.WriteJSON(w, http.StatusOK, map[string]any{"id": id, "type": "rbac_group_deleted"})
}

// addMember applies Add RBAC Group Member: the body carries user_id, the
// answer is the membership; an unknown group is a 404.
func (f *rbacFake) addMember(w http.ResponseWriter, r *http.Request) {
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	gid := r.PathValue("rbac_group_id")
	f.mu.Lock()
	defer f.mu.Unlock()
	if find(f.groups, gid) < 0 {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "rbac group not found")
		return
	}
	m := memberJSON(gid, fmt.Sprint(body["user_id"]))
	f.members = append(f.members, m)
	sdktest.WriteJSON(w, http.StatusOK, m)
}

// listMembers pages a group's members by user_id; an unknown group is a 404.
func (f *rbacFake) listMembers(w http.ResponseWriter, r *http.Request) {
	gid := r.PathValue("rbac_group_id")
	if f.group(gid) == nil {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "rbac group not found")
		return
	}
	f.page(w, r, f.snapshot(&f.members, func(m map[string]any) bool { return m["rbac_group_id"] == gid }), "user_id")
}

// removeMember answers {"rbac_group_id", "type": "rbac_group_member_deleted",
// "user_id"}.
func (f *rbacFake) removeMember(w http.ResponseWriter, r *http.Request) {
	gid, uid := r.PathValue("rbac_group_id"), r.PathValue("user_id")
	if !f.hasMember(gid, uid) {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "rbac group member not found")
		return
	}
	f.dropMember(gid, uid)
	sdktest.WriteJSON(w, http.StatusOK, map[string]any{"rbac_group_id": gid, "type": "rbac_group_member_deleted", "user_id": uid})
}

// page answers one page of items with the kit's paginator (page/next_page,
// cursor checks), the cursors naming the field key, plus the has_more every
// RBAC list sends next to next_page, which rbac.go stops on.
func (f *rbacFake) page(w http.ResponseWriter, r *http.Request, items []map[string]any, key string) {
	rec := httptest.NewRecorder()
	f.PaginateBy(rec, r, sdktest.PageToken, f.pageSize, items, func(it map[string]any) string { return fmt.Sprint(it[key]) })
	var page map[string]any
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &page) != nil {
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
		return
	}
	page["has_more"] = page["next_page"] != nil
	sdktest.WriteJSON(w, http.StatusOK, page)
}

// assertRBACRequests checks that every request the fake received carried
// the Admin key alone, no anthropic-beta header, and ?beta=true.
func (f *rbacFake) assertRBACRequests(t *testing.T) {
	t.Helper()
	f.AssertShape(sdktest.Shape{Credential: sdktest.AdminKey, BetaQuery: true})
}
