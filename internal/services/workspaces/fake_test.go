// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces_test

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"

	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// Routes of the Admin API workspace endpoints (Admin key, no beta header).
// References: https://platform.claude.com/docs/en/api/organization/workspaces/{create,list,retrieve,update,archive},
// .../workspaces/members/{add,list,retrieve,update,remove} and
// .../workspaces/rate_limits/{list,update}.
const (
	routeCreateWorkspace  = "POST /v1/organizations/workspaces"
	routeListWorkspaces   = "GET /v1/organizations/workspaces"
	routeGetWorkspace     = "GET /v1/organizations/workspaces/{workspace_id}"
	routeUpdateWorkspace  = "POST /v1/organizations/workspaces/{workspace_id}"
	routeArchiveWorkspace = "POST /v1/organizations/workspaces/{workspace_id}/archive"
	routeAddMember        = "POST /v1/organizations/workspaces/{workspace_id}/members"
	routeListMembers      = "GET /v1/organizations/workspaces/{workspace_id}/members"
	routeGetMember        = "GET /v1/organizations/workspaces/{workspace_id}/members/{user_id}"
	routeUpdateMember     = "POST /v1/organizations/workspaces/{workspace_id}/members/{user_id}"
	routeRemoveMember     = "DELETE /v1/organizations/workspaces/{workspace_id}/members/{user_id}"
	routeListWSLimits     = "GET /v1/organizations/workspaces/{workspace_id}/rate_limits"
	routeSetWSLimits      = "POST /v1/organizations/workspaces/{workspace_id}/rate_limits/{rate_limit_id}"
)

// wsFake serves workspaces, their members and their rate-limit overrides.
type wsFake struct {
	*sdktest.Server
	pageSize int

	mu         sync.Mutex
	created    int
	workspaces []map[string]any // creation order
	defaultID  string           // the Default Workspace, listed only with include_default
	members    []map[string]any // WorkspaceMember objects, in insertion order
	orgLimits  []orgLimit
	// overrides holds the workspace overrides: workspace ID, rate_limit_id,
	// limiter type.
	overrides map[string]map[string]map[string]int64
}

func newWSFake(t *testing.T, pageSize int) *wsFake {
	t.Helper()
	f := &wsFake{Server: sdktest.NewServer(t), pageSize: pageSize, overrides: map[string]map[string]map[string]int64{}}
	f.Handle(routeCreateWorkspace, f.createWorkspace)
	f.Handle(routeListWorkspaces, f.listWorkspaces)
	f.Handle(routeGetWorkspace, f.getWorkspace)
	f.Handle(routeUpdateWorkspace, f.updateWorkspace)
	f.Handle(routeArchiveWorkspace, f.archiveWorkspace)
	f.Handle(routeAddMember, f.addMember)
	f.Handle(routeListMembers, f.listMembers)
	f.Handle(routeGetMember, f.getMember)
	f.Handle(routeUpdateMember, f.updateMember)
	f.Handle(routeRemoveMember, f.removeMember)
	f.Handle(routeListWSLimits, f.listWorkspaceLimits)
	f.Handle(routeSetWSLimits, f.setWorkspaceLimits)
	return f
}

func workspaceID(n int) string { return fmt.Sprintf("wrkspc_%024d", n) }

// workspaceJSON is a Workspace object with the documented create defaults
// ("defaults to workspace_geo: us, allowed_inference_geos: unrestricted,
// and default_inference_geo: global"), tags {} and no key. compartment_id is
// a string per the reference; inference_data_retention is the shape observed
// live (internal/services/workspaces/CLAUDE.md), not in the reference.
func workspaceJSON(n int, name string) map[string]any {
	return map[string]any{
		"id":            workspaceID(n),
		"name":          name,
		"archived_at":   nil,
		"created_at":    fmt.Sprintf("2026-04-%02dT10:00:00Z", n),
		"display_color": "#6C5BB9",
		"type":          "workspace",
		"data_residency": map[string]any{
			"allowed_inference_geos": "unrestricted", "default_inference_geo": "global", "workspace_geo": "us",
		},
		"tags":                     map[string]any{},
		"external_key_id":          nil,
		"compartment_id":           fmt.Sprintf("f8a7b6c5-4d3e-4f1a-8b9c-%012d", n),
		"inference_data_retention": map[string]any{"type": "disabled"},
	}
}

// setDefault marks id as the organization's Default Workspace.
func (f *wsFake) setDefault(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.defaultID = id
}

// putWorkspace seeds a workspace created outside Terraform.
func (f *wsFake) putWorkspace(ws map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created++
	f.workspaces = append(f.workspaces, ws)
}

// workspace returns the stored workspace id, nil when unknown. The caller
// holds f.mu.
func (f *wsFake) workspace(id string) map[string]any {
	for _, ws := range f.workspaces {
		if ws["id"] == id {
			return ws
		}
	}
	return nil
}

// getWS returns a copy of a stored workspace, for test assertions.
func (f *wsFake) getWS(id string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return copyJSON(f.workspace(id))
}

func (f *wsFake) deleteWS(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.workspaces = slices.DeleteFunc(f.workspaces, func(ws map[string]any) bool { return ws["id"] == id })
}

func (f *wsFake) setArchived(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.workspace(id)["archived_at"] = "2026-05-01T00:00:00Z"
}

// decode reads a JSON object body, or answers 400.
func decode(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", "body is not a JSON object")
		return nil, false
	}
	return body, true
}

// createWorkspace applies Create Workspace: name required (1..255), the
// data_residency, tags and external_key_id of the body over the defaults.
func (f *wsFake) createWorkspace(w http.ResponseWriter, r *http.Request) {
	body, ok := decode(w, r)
	if !ok {
		return
	}
	name, _ := body["name"].(string)
	if name == "" || len(name) > 255 {
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", "name: must be 1 to 255 characters")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.created++
	ws := workspaceJSON(f.created, name)
	if dr, ok := body["data_residency"].(map[string]any); ok {
		maps.Copy(ws["data_residency"].(map[string]any), dr)
	}
	if tags, ok := body["tags"].(map[string]any); ok {
		ws["tags"] = tags
	}
	if key, ok := body["external_key_id"].(string); ok {
		ws["external_key_id"] = key
	}
	f.workspaces = append(f.workspaces, ws)
	sdktest.WriteJSON(w, http.StatusOK, ws)
}

func (f *wsFake) getWorkspace(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ws := f.workspace(r.PathValue("workspace_id"))
	if ws == nil {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "workspace not found")
		return
	}
	sdktest.WriteJSON(w, http.StatusOK, ws)
}

// updateWorkspace applies Update Workspace. The tags semantics are the ones
// measured live (CLAUDE.md of the service): the API merges tags, a per-key
// null deletes the key, and a whole-map null is a 400. external_key_id is
// write-once ("once a key is attached to a Workspace it cannot be detached
// or replaced").
func (f *wsFake) updateWorkspace(w http.ResponseWriter, r *http.Request) {
	body, ok := decode(w, r)
	if !ok {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	ws := f.workspace(r.PathValue("workspace_id"))
	if ws == nil {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "workspace not found")
		return
	}
	if v, has := body["tags"]; has && v == nil {
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", "At least one field must be provided")
		return
	}
	if key, has := body["external_key_id"]; has && ws["external_key_id"] != nil && key != ws["external_key_id"] {
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", "external_key_id cannot be changed once set")
		return
	}
	for field, v := range body {
		switch field {
		case "tags":
			tags := ws["tags"].(map[string]any)
			for k, tv := range v.(map[string]any) {
				if tv == nil {
					delete(tags, k)
				} else {
					tags[k] = tv
				}
			}
		case "data_residency":
			maps.Copy(ws["data_residency"].(map[string]any), v.(map[string]any))
		default:
			ws[field] = v
		}
	}
	sdktest.WriteJSON(w, http.StatusOK, ws)
}

// archiveWorkspace applies Archive Workspace: archived_at is set and the
// workspace returned.
func (f *wsFake) archiveWorkspace(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	ws := f.workspace(r.PathValue("workspace_id"))
	if ws == nil {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "workspace not found")
		return
	}
	if ws["archived_at"] == nil {
		ws["archived_at"] = fmt.Sprintf("2026-06-%02dT00:00:00Z", f.created)
	}
	sdktest.WriteJSON(w, http.StatusOK, ws)
}

// listWorkspaces applies List Workspaces: after_id pages; archived
// workspaces and the Default Workspace only with include_archived and
// include_default ("default: false").
func (f *wsFake) listWorkspaces(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f.mu.Lock()
	var items []map[string]any
	for _, ws := range f.workspaces {
		if (ws["archived_at"] != nil && q.Get("include_archived") != "true") || (ws["id"] == f.defaultID && q.Get("include_default") != "true") {
			continue
		}
		items = append(items, copyJSON(ws))
	}
	f.mu.Unlock()
	f.Paginate(w, r, sdktest.AfterID, f.pageSize, items)
}

// --- members ---

// member returns the index of a stored member, -1 when unknown. The caller
// holds f.mu.
func (f *wsFake) member(workspaceID, userID string) int {
	return slices.IndexFunc(f.members, func(m map[string]any) bool {
		return m["workspace_id"] == workspaceID && m["user_id"] == userID
	})
}

func memberJSON(workspaceID, userID, role string) map[string]any {
	return map[string]any{"type": "workspace_member", "user_id": userID, "workspace_id": workspaceID, "workspace_role": role}
}

func (f *wsFake) putMember(m map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.members = append(f.members, m)
}

func (f *wsFake) getMemberRole(workspaceID, userID string) (string, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i := f.member(workspaceID, userID); i >= 0 {
		return f.members[i]["workspace_role"].(string), true
	}
	return "", false
}

func (f *wsFake) dropMember(workspaceID, userID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if i := f.member(workspaceID, userID); i >= 0 {
		f.members = slices.Delete(f.members, i, i+1)
	}
}

// addMember applies Create Workspace Member: workspace_role "Cannot be
// workspace_billing".
func (f *wsFake) addMember(w http.ResponseWriter, r *http.Request) {
	body, ok := decode(w, r)
	if !ok {
		return
	}
	role, _ := body["workspace_role"].(string)
	user, _ := body["user_id"].(string)
	if user == "" || role == "" || role == "workspace_billing" {
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", "user_id and a non-billing workspace_role are required")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	wsID := r.PathValue("workspace_id")
	if f.workspace(wsID) == nil {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "workspace not found")
		return
	}
	m := memberJSON(wsID, user, role)
	f.members = append(f.members, m)
	sdktest.WriteJSON(w, http.StatusOK, m)
}

func (f *wsFake) getMember(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	i := f.member(r.PathValue("workspace_id"), r.PathValue("user_id"))
	if i < 0 {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "workspace member not found")
		return
	}
	sdktest.WriteJSON(w, http.StatusOK, f.members[i])
}

func (f *wsFake) updateMember(w http.ResponseWriter, r *http.Request) {
	body, ok := decode(w, r)
	if !ok {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	i := f.member(r.PathValue("workspace_id"), r.PathValue("user_id"))
	if i < 0 {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "workspace member not found")
		return
	}
	f.members[i]["workspace_role"] = body["workspace_role"]
	sdktest.WriteJSON(w, http.StatusOK, f.members[i])
}

// removeMember answers {"type": "workspace_member_deleted", "user_id",
// "workspace_id"}.
func (f *wsFake) removeMember(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	wsID, user := r.PathValue("workspace_id"), r.PathValue("user_id")
	i := f.member(wsID, user)
	if i < 0 {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "workspace member not found")
		return
	}
	f.members = slices.Delete(f.members, i, i+1)
	sdktest.WriteJSON(w, http.StatusOK, map[string]any{"type": "workspace_member_deleted", "user_id": user, "workspace_id": wsID})
}

// listMembers applies List Workspace Members: after_id pages, the user_id
// as the cursor (a WorkspaceMember has no other ID).
func (f *wsFake) listMembers(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	var items []map[string]any
	for _, m := range f.members {
		if m["workspace_id"] == r.PathValue("workspace_id") {
			items = append(items, copyJSON(m))
		}
	}
	f.mu.Unlock()
	f.paginateBy(w, r, sdktest.AfterID, "user_id", items)
}

// paginateBy serves items with the kit's paginator and its cursor checks,
// using field as the cursor ID of objects that have no "id"; the "id" the
// kit needs is removed from the answer.
func (f *wsFake) paginateBy(w http.ResponseWriter, r *http.Request, family sdktest.Family, field string, items []map[string]any) {
	withID := make([]map[string]any, len(items))
	for i, it := range items {
		withID[i] = copyJSON(it)
		withID[i]["id"] = it[field]
	}
	rec := httptest.NewRecorder()
	f.Paginate(rec, r, family, f.pageSize, withID)
	var page map[string]any
	if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &page) != nil {
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
		return
	}
	for _, it := range page["data"].([]any) {
		delete(it.(map[string]any), "id")
	}
	sdktest.WriteJSON(w, http.StatusOK, page)
}

func copyJSON(o map[string]any) map[string]any {
	if o == nil {
		return nil
	}
	b, _ := json.Marshal(o)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return out
}

func expectAction(addr string, action plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{
		PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, action)},
		PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
	}
}

// assertAdminRequests checks that every request the fake received carried
// the Admin key alone and no anthropic-beta header (the endpoints are GA).
func (f *wsFake) assertAdminRequests(t *testing.T) {
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

// paths returns the path of every request to route, with its fault.
func (f *wsFake) paths(route string) string {
	var out []string
	for _, r := range f.Requests(route) {
		out = append(out, strings.TrimSpace(r.Path+" "+r.Fault))
	}
	return strings.Join(out, ", ")
}

// --- rate limits ---

// orgLimit is one of the organization's rate-limit entries, which a workspace
// inherits and overrides by its id.
type orgLimit struct {
	id, groupType, displayName string
	models                     []string
	limits                     map[string]int64
}

func rateLimitID(n int) string { return fmt.Sprintf("rl_%024d", n) }

func groupID(groupType string) string { return "rlg_" + groupType }

// group is the entry's group: model_group carries a display_name, the other
// types an id and a type only.
func (l orgLimit) group() map[string]any {
	g := map[string]any{"id": groupID(l.groupType), "type": l.groupType}
	if l.groupType == "model_group" {
		g["display_name"] = l.displayName
	}
	return g
}

// models is the entry's models: "null when group.type is not model_group".
func (l orgLimit) modelsJSON() any {
	if l.groupType != "model_group" {
		return nil
	}
	return l.models
}

func (f *wsFake) putOrgLimit(l orgLimit) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.orgLimits = append(f.orgLimits, l)
}

// setOverride stores a workspace override, as the Console would.
func (f *wsFake) setOverride(wsID, rlID, limiter string, v int64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setOverrideLocked(wsID, rlID, limiter, v)
}

func (f *wsFake) setOverrideLocked(wsID, rlID, limiter string, v int64) {
	if f.overrides[wsID] == nil {
		f.overrides[wsID] = map[string]map[string]int64{}
	}
	if f.overrides[wsID][rlID] == nil {
		f.overrides[wsID][rlID] = map[string]int64{}
	}
	f.overrides[wsID][rlID][limiter] = v
}

func (f *wsFake) clearOverride(wsID, rlID, limiter string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.overrides[wsID][rlID], limiter)
}

func (f *wsFake) overridesOf(wsID, rlID string) map[string]int64 {
	f.mu.Lock()
	defer f.mu.Unlock()
	return maps.Clone(f.overrides[wsID][rlID])
}

// workspaceEntry is the WorkspaceRateLimit entry of l in workspace wsID:
// the stored overrides (source workspace, org_limit for reference, "null
// when the organization has no limit configured for this limiter type") and,
// with inherited, the organization's other values (source organization,
// value equal to org_limit). Without inherited a group without override is
// not listed. The caller holds f.mu.
func (f *wsFake) workspaceEntry(wsID string, l orgLimit, inherited bool) (map[string]any, bool) {
	over := f.overrides[wsID][l.id]
	if len(over) == 0 && !inherited {
		return nil, false
	}
	types := map[string]int64{}
	maps.Copy(types, over)
	if inherited {
		maps.Copy(types, l.limits)
		maps.Copy(types, over)
	}
	limits := []map[string]any{}
	for _, t := range slices.Sorted(maps.Keys(types)) {
		var orgLimit any
		if v, ok := l.limits[t]; ok {
			orgLimit = v
		}
		if v, ok := over[t]; ok {
			limits = append(limits, map[string]any{"type": t, "value": v, "org_limit": orgLimit, "source": map[string]any{"type": "workspace"}})
		} else {
			limits = append(limits, map[string]any{"type": t, "value": l.limits[t], "org_limit": orgLimit, "source": map[string]any{"type": "organization"}})
		}
	}
	return map[string]any{
		"type": "workspace_rate_limit", "rate_limit_id": l.id, "workspace_id": wsID,
		"group": l.group(), "group_type": l.groupType, "models": l.modelsJSON(), "limits": limits,
	}, true
}

// listWorkspaceLimits applies List Workspace Rate Limits: page/next_page
// pages (the rate_limit_id as cursor), group_type and include_inherited
// filters; an unknown workspace is a 404.
func (f *wsFake) listWorkspaceLimits(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	wsID := r.PathValue("workspace_id")
	f.mu.Lock()
	if f.workspace(wsID) == nil {
		f.mu.Unlock()
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "workspace not found")
		return
	}
	var items []map[string]any
	for _, l := range f.orgLimits {
		if gt := q.Get("group_type"); gt != "" && l.groupType != gt {
			continue
		}
		if e, ok := f.workspaceEntry(wsID, l, q.Get("include_inherited") == "true"); ok {
			items = append(items, e)
		}
	}
	f.mu.Unlock()
	f.paginateBy(w, r, sdktest.PageToken, "rate_limit_id", items)
}

// workspaceRateLimitTypes is the enum of WorkspaceRateLimitType.
var workspaceRateLimitTypes = []string{
	"fast_itpmca", "fast_otpm", "input_tokens_per_minute", "input_tokens_per_minute_cache_aware",
	"output_tokens_per_minute", "requests_per_minute", "tokens_per_day", "tokens_per_minute", "tool_uses_per_second",
}

// setWorkspaceLimits applies Set Workspace Rate Limits: limits has 1 to 16
// items, an integer value sets the override and null clears it, types not
// named keep their value; the answer is the group's entry.
func (f *wsFake) setWorkspaceLimits(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Limits []struct {
			Type  string `json:"type"`
			Value *int64 `json:"value"`
		} `json:"limits"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Limits) < 1 || len(body.Limits) > 16 {
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", "limits: 1 to 16 items required")
		return
	}
	wsID, rlID := r.PathValue("workspace_id"), r.PathValue("rate_limit_id")
	f.mu.Lock()
	defer f.mu.Unlock()
	i := slices.IndexFunc(f.orgLimits, func(l orgLimit) bool { return l.id == rlID })
	if f.workspace(wsID) == nil || i < 0 {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "workspace or rate limit not found")
		return
	}
	for _, l := range body.Limits {
		if !slices.Contains(workspaceRateLimitTypes, l.Type) {
			sdktest.WriteAPIError(w, http.StatusBadRequest, "", "unknown limiter type "+l.Type)
			return
		}
	}
	for _, l := range body.Limits {
		if l.Value == nil {
			delete(f.overrides[wsID][rlID], l.Type)
		} else {
			f.setOverrideLocked(wsID, rlID, l.Type, *l.Value)
		}
	}
	e, ok := f.workspaceEntry(wsID, f.orgLimits[i], false)
	if !ok {
		e, _ = f.workspaceEntry(wsID, orgLimit{id: rlID, groupType: f.orgLimits[i].groupType, displayName: f.orgLimits[i].displayName, models: f.orgLimits[i].models}, true)
	}
	sdktest.WriteJSON(w, http.StatusOK, e)
}
