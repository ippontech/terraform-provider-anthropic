// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package plugins_test

import (
	"archive/zip"
	"bytes"
	"fmt"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/knownvalue"

	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// Routes of the Plugins API (Admin API key in x-api-key, beta header on
// every request). References, under
// https://platform.claude.com/docs/en/api/beta/organization/:
// plugins/{create,retrieve,update,delete,list}, plugins/versions/{create,
// retrieve,list}, plugins/installation_settings/{set,remove,list} and
// plugin_marketplaces/{retrieve,update,list}; and the guide
// https://platform.claude.com/docs/en/manage-claude/plugins-api.
const (
	routeCreatePlugin      = "POST /v1/organizations/plugins"
	routeListPlugins       = "GET /v1/organizations/plugins"
	routeGetPlugin         = "GET /v1/organizations/plugins/{plugin_id}"
	routeUpdatePlugin      = "POST /v1/organizations/plugins/{plugin_id}"
	routeDeletePlugin      = "DELETE /v1/organizations/plugins/{plugin_id}"
	routeCreateVersion     = "POST /v1/organizations/plugins/{plugin_id}/versions"
	routeListVersions      = "GET /v1/organizations/plugins/{plugin_id}/versions"
	routeGetVersion        = "GET /v1/organizations/plugins/{plugin_id}/versions/{version_id}"
	routeListSettings      = "GET /v1/organizations/plugins/{plugin_id}/installation_settings"
	routeSetSetting        = "POST /v1/organizations/plugins/{plugin_id}/installation_settings/{target}"
	routeRemoveSetting     = "DELETE /v1/organizations/plugins/{plugin_id}/installation_settings/{target}"
	routeListMarketplaces  = "GET /v1/organizations/plugin_marketplaces"
	routeGetMarketplace    = "GET /v1/organizations/plugin_marketplaces/{marketplace_id}"
	routeUpdateMarketplace = "POST /v1/organizations/plugin_marketplaces/{marketplace_id}"

	// pluginsBeta is the beta header every request must carry: "A request
	// without it returns 404, exactly as if the endpoint did not exist" (every
	// Plugins API page).
	pluginsBeta = "ce-plugins-2026-09-01"

	// libraryMarketplaceID is where an upload that names no marketplace goes
	// (guide, Marketplaces).
	libraryMarketplaceID = "marketplace_library"

	// uploaderKeyID is the api_key_id of the created_by of what the API
	// stores for an Admin key upload ("api_actor").
	uploaderKeyID = "apikey_sdktest"
)

var allRoutes = []string{
	routeCreatePlugin, routeListPlugins, routeGetPlugin, routeUpdatePlugin, routeDeletePlugin,
	routeCreateVersion, routeListVersions, routeGetVersion, routeListSettings, routeSetSetting, routeRemoveSetting,
	routeListMarketplaces, routeGetMarketplace, routeUpdateMarketplace,
}

var baseTime = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

// pluginsFake is the Plugins API of one Claude Enterprise organization. It
// never reads the uploaded files: what an upload stores (the manifest's
// name, the display fields, components, reach and scan) is the canned
// upload a test sets, salesToolkit by default. Lists answer newest first.
type pluginsFake struct {
	*sdktest.Server
	t        *testing.T
	pageSize int // largest page the fake answers below the request's limit; 0 for the limit alone

	mu           sync.Mutex
	clock, seq   int
	plugins      []map[string]any            // creation order
	versions     map[string][]map[string]any // by plugin ID, creation order
	settings     map[string][]map[string]any // by organization-owned plugin ID, creation order
	marketplaces []map[string]any            // creation order
	upload       map[string]any              // what the next uploads store
	pending      bool                        // the next create answers registration_pending
	canned       map[string][]cannedError    // by route, answered before the handler
}

type cannedError struct {
	status  int
	message string
	details map[string]any
}

func newPluginsFake(t *testing.T, pageSize int) *pluginsFake {
	t.Helper()
	f := &pluginsFake{
		Server: sdktest.NewServer(t), t: t, pageSize: pageSize, upload: salesToolkit(),
		versions: map[string][]map[string]any{}, settings: map[string][]map[string]any{},
		canned: map[string][]cannedError{},
	}
	for route, h := range map[string]http.HandlerFunc{
		routeCreatePlugin: f.createPlugin, routeListPlugins: f.listPlugins, routeGetPlugin: f.getPlugin,
		routeUpdatePlugin: f.updatePlugin, routeDeletePlugin: f.deletePlugin, routeCreateVersion: f.createVersion,
		routeListVersions: f.listVersions, routeGetVersion: f.getVersion, routeListSettings: f.listSettings,
		routeSetSetting: f.setSetting, routeRemoveSetting: f.removeSetting, routeListMarketplaces: f.listMarketplaces,
		routeGetMarketplace: f.getMarketplace, routeUpdateMarketplace: f.updateMarketplace,
	} {
		f.Handle(route, sdktest.RequireBeta(pluginsBeta, f.cannedFirst(route, h)))
	}
	return f
}

// salesToolkit is what the API reads from the plugin writePluginDir writes.
func salesToolkit() map[string]any {
	return map[string]any{
		"name": "sales-toolkit", "display_name": "Sales Toolkit", "description": "Account research and call prep.",
		"manifest_version": "1.4.0", "reach": "contained", "content_scan": passed(),
		"components":                           []any{map[string]any{"type": "skill", "name": "account-research", "description": "Researches a customer account."}},
		"organization_installation_preference": "not_available", "organization_installation_preference_inherited": true,
	}
}

// passed is the content_scan of a version that may be served.
func passed() map[string]any {
	return map[string]any{"status": "completed", "assessment": "pass", "reason": nil}
}

// versionFields are the fields of an upload a version keeps; the plugin a
// create stores keeps them all. A plugin's display fields "describe the
// served version": the fake leaves them as the create stored them.
var versionFields = []string{"display_name", "description", "manifest_version", "components", "reach", "content_scan"}

// setUpload changes what the next uploads store.
func (f *pluginsFake) setUpload(fields map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	maps.Copy(f.upload, fields)
}

// failNext answers the next request to route with status, message and the
// details the Plugins API adds "when one status has several causes that you
// would handle differently" (Error responses,
// https://platform.claude.com/docs/en/manage-claude/plugins-api):
// createPlugin and servePluginVersion branch on details.error_code.
func (f *pluginsFake) failNext(route string, status int, message string, details map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.canned[route] = append(f.canned[route], cannedError{status, message, details})
}

func (f *pluginsFake) cannedFirst(route string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		queue := f.canned[route]
		if len(queue) > 0 {
			f.canned[route] = queue[1:]
		}
		f.mu.Unlock()
		if len(queue) == 0 {
			h(w, r)
			return
		}
		writeError(w, queue[0].status, queue[0].message, queue[0].details)
	}
}

// writeError writes the error envelope, with details when there are any.
func writeError(w http.ResponseWriter, status int, message string, details map[string]any) {
	if details == nil {
		sdktest.WriteAPIError(w, status, "", message)
		return
	}
	sdktest.WriteJSON(w, status, map[string]any{
		"type":       "error",
		"error":      map[string]any{"type": sdktest.ErrorType(status), "message": message, "details": details},
		"request_id": w.Header().Get("request-id"),
	})
}

func (f *pluginsFake) nowLocked() string {
	f.clock++
	return baseTime.Add(time.Duration(f.clock) * time.Minute).Format(time.RFC3339)
}

func (f *pluginsFake) newIDLocked(prefix string) string {
	f.seq++
	return fmt.Sprintf("%s%04d", prefix, f.seq)
}

// --- Seeding and changes outside Terraform ---

// putMarketplace stores a marketplace as the API returns it: owner
// organization (userID "") or a member, source manual or a Git host.
func (f *pluginsFake) putMarketplace(id, name, userID, source, defaultPreference string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := map[string]any{
		"type": "plugin_marketplace", "id": id, "name": name, "owner": map[string]any{"type": "organization"},
		"source": source, "sync_status": nil, "last_sync_ended_at": nil, "last_sync_read_sha": nil,
		"default_installation_preference": defaultPreference, "created_at": f.nowLocked(),
	}
	if defaultPreference == "" {
		m["default_installation_preference"] = "not_available" // "(not_available if never set)"
	}
	if userID != "" {
		m["owner"] = map[string]any{"type": "user", "user_id": userID}
		m["default_installation_preference"] = nil // "Null for a member's personal plugin marketplace"
	}
	if source != "manual" {
		m["sync_status"], m["last_sync_ended_at"], m["last_sync_read_sha"] = "success", "2026-09-10T22:15:03Z", "9fceb02d0ae598e95dc970b74767f19372d61af8"
	}
	f.marketplaces = append(f.marketplaces, m)
}

// putOrgPlugin stores an organization-owned plugin with one version in the
// library marketplace, as an upload outside Terraform does, and returns the
// plugin's ID and the version's.
func (f *pluginsFake) putOrgPlugin(name string) (string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.storePluginLocked(map[string]any{
		"name": name, "display_name": "Seeded", "description": nil, "manifest_version": "1.0.0", "components": []any{},
		"reach": "contained", "content_scan": passed(),
		"organization_installation_preference": "not_available", "organization_installation_preference_inherited": true,
	}, libraryMarketplaceID, "")
	return p["id"].(string), p["served_version_id"].(string)
}

// putMemberPlugin stores a plugin in the personal marketplace of userID,
// with one version.
func (f *pluginsFake) putMemberPlugin(id, name, userID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	member := map[string]any{"type": "user_actor", "user_id": userID, "email_address": nil}
	p := map[string]any{
		"type": "plugin", "id": id, "name": name, "owner": map[string]any{"type": "user", "user_id": userID},
		"marketplace_id": "marketplace_" + userID, "served_version_pinned": false, "created_at": f.nowLocked(), "created_by": member,
		"organization_installation_preference": nil, "organization_installation_preference_inherited": nil,
		"display_name": nil, "description": nil, "manifest_version": nil, "components": []any{}, "reach": "contained",
		"content_scan": passed(),
	}
	f.plugins = append(f.plugins, p)
	f.storeVersionLocked(p, p, "")["created_by"] = member
}

// uploadVersionOutOfBand stores a new version of a plugin, as an upload in
// claude.ai does, and returns its ID. fields override what it stores.
func (f *pluginsFake) uploadVersionOutOfBand(pluginID string, fields map[string]any) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	content := map[string]any{"display_name": "Sales Toolkit", "description": nil, "components": []any{}, "reach": "contained", "content_scan": passed()}
	maps.Copy(content, fields)
	return f.storeVersionLocked(f.pluginLocked(pluginID), content, "")["id"].(string)
}

// deletePluginOutOfBand deletes a plugin as an administrator does in
// claude.ai.
func (f *pluginsFake) deletePluginOutOfBand(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dropPluginLocked(id)
}

// putSetting stores a setting written outside Terraform.
func (f *pluginsFake) putSetting(pluginID, target, preference string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.setLocked(pluginID, target, preference)
}

// removeSettingOutOfBand removes a setting as an administrator does in
// claude.ai.
func (f *pluginsFake) removeSettingOutOfBand(pluginID, target string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.settings[pluginID] = slices.DeleteFunc(f.settings[pluginID], func(s map[string]any) bool { return settingTarget(s) == target })
}

// setMarketplaceDefaultOutOfBand changes a marketplace's default as an
// administrator does in claude.ai.
func (f *pluginsFake) setMarketplaceDefaultOutOfBand(id, preference string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.marketplaceLocked(id)["default_installation_preference"] = preference
}

// deleteMarketplaceOutOfBand removes a marketplace as claude.ai does.
func (f *pluginsFake) deleteMarketplaceOutOfBand(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.marketplaces = slices.DeleteFunc(f.marketplaces, func(m map[string]any) bool { return m["id"] == id })
}

// answerRegistrationPending makes the next create store the plugin but
// answer 503 registration_pending.
func (f *pluginsFake) answerRegistrationPending() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pending = true
}

// --- Reading the fake ---

// plugin returns a copy of a stored plugin, or nil.
func (f *pluginsFake) plugin(id string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p := f.pluginLocked(id); p != nil {
		return sdktest.DeepCopy(p)
	}
	return nil
}

// versionCount returns the number of versions a plugin holds.
func (f *pluginsFake) versionCount(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.versions[id])
}

// setting returns the setting a plugin holds for target, or nil.
func (f *pluginsFake) setting(pluginID, target string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.settings[pluginID] {
		if settingTarget(s) == target {
			return sdktest.DeepCopy(s)
		}
	}
	return nil
}

// marketplaceDefault returns a marketplace's default_installation_preference.
func (f *pluginsFake) marketplaceDefault(id string) any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.marketplaceLocked(id)["default_installation_preference"]
}

// --- Storage ---

func (f *pluginsFake) pluginLocked(id string) map[string]any {
	i := slices.IndexFunc(f.plugins, func(p map[string]any) bool { return p["id"] == id })
	if i < 0 {
		return nil
	}
	return f.plugins[i]
}

func (f *pluginsFake) marketplaceLocked(id string) map[string]any {
	i := slices.IndexFunc(f.marketplaces, func(m map[string]any) bool { return m["id"] == id })
	if i < 0 {
		return nil
	}
	return f.marketplaces[i]
}

func (f *pluginsFake) dropPluginLocked(id string) {
	f.plugins = slices.DeleteFunc(f.plugins, func(p map[string]any) bool { return p["id"] == id })
	delete(f.versions, id)
	delete(f.settings, id)
}

// storePluginLocked stores an organization-owned plugin with the fields of
// an upload, and its first version.
func (f *pluginsFake) storePluginLocked(upload map[string]any, marketplaceID, releaseNotes string) map[string]any {
	p := sdktest.DeepCopy(upload)
	maps.Copy(p, map[string]any{
		"type": "plugin", "id": f.newIDLocked("plugin_"), "owner": map[string]any{"type": "organization"},
		"marketplace_id": marketplaceID, "served_version_pinned": false, "created_at": f.nowLocked(),
		"created_by": map[string]any{"type": "api_actor", "api_key_id": uploaderKeyID},
	})
	f.plugins = append(f.plugins, p)
	f.settings[p["id"].(string)] = []map[string]any{}
	f.storeVersionLocked(p, upload, releaseNotes)
	return p
}

// storeVersionLocked stores a version of p with the version fields of
// upload. It becomes the latest version and, unless p is pinned, the served
// one: "it becomes the version served to members unless the Plugin's served
// version has been pinned"
// (https://platform.claude.com/docs/en/api/beta/organization/plugins/versions/create).
// mapPluginToState maps both IDs, and PluginResource.Update reads them with
// served_version_pinned to decide whether an apply serves the configured
// version.
func (f *pluginsFake) storeVersionLocked(p, upload map[string]any, releaseNotes string) map[string]any {
	v := map[string]any{
		"type": "plugin_version", "id": f.newIDLocked("pluginver_"), "plugin_id": p["id"], "release_notes": nil,
		"created_by": map[string]any{"type": "api_actor", "api_key_id": uploaderKeyID}, "created_at": f.nowLocked(),
	}
	for _, k := range versionFields {
		v[k] = upload[k]
	}
	if releaseNotes != "" {
		v["release_notes"] = releaseNotes
	}
	v = sdktest.DeepCopy(v)
	id := p["id"].(string)
	f.versions[id] = append(f.versions[id], v)
	p["latest_version_id"] = v["id"]
	if pinned, _ := p["served_version_pinned"].(bool); !pinned {
		p["served_version_id"] = v["id"]
	}
	p["updated_at"] = v["created_at"]
	return v
}

// uploadFields reads the form fields of a multipart upload (the files[]
// parts are what uploadedFiles checks), or answers 400.
func uploadFields(w http.ResponseWriter, r *http.Request) (map[string]string, bool) {
	parts, err := sdktest.ReadParts(r)
	if err != nil {
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", "body is not multipart/form-data: "+err.Error())
		return nil, false
	}
	fields := map[string]string{}
	for _, p := range parts {
		if p.Field != "files[]" {
			fields[p.Field] = string(p.Content)
		}
	}
	return fields, true
}

// --- Plugins ---

// createPlugin stores an organization-owned plugin from the canned upload,
// in marketplace_id or the library marketplace.
func (f *pluginsFake) createPlugin(w http.ResponseWriter, r *http.Request) {
	fields, ok := uploadFields(w, r)
	if !ok {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	marketplaceID := fields["marketplace_id"]
	if marketplaceID == "" {
		marketplaceID = libraryMarketplaceID
	}
	p := f.storePluginLocked(f.upload, marketplaceID, fields["release_notes"])
	if f.pending {
		// "A 503 with error_code registration_pending means the Plugin and
		// its version were stored (their IDs are in details) but are not yet
		// usable in claude.ai: do not retry the create; create a version on
		// the stored Plugin instead, which completes it"
		// (https://platform.claude.com/docs/en/api/beta/organization/plugins/create).
		// createPlugin and completePluginRegistration depend on it.
		f.pending = false
		writeError(w, http.StatusServiceUnavailable, "the plugin's skills are not registered yet",
			map[string]any{"error_code": "registration_pending", "plugin_id": p["id"], "plugin_version_id": p["served_version_id"]})
		return
	}
	sdktest.WriteJSON(w, http.StatusOK, p)
}

func (f *pluginsFake) getPlugin(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p := f.pluginLocked(r.PathValue("plugin_id")); p != nil {
		sdktest.WriteJSON(w, http.StatusOK, p)
		return
	}
	sdktest.WriteAPIError(w, http.StatusNotFound, "", "plugin not found")
}

// updatePlugin serves served_version_id and pins the plugin, which moves
// updated_at "on a served-version change, including pinning an unpinned
// Plugin to the version it already serves"
// (https://platform.claude.com/docs/en/api/beta/organization/plugins/update).
// PluginResource.ModifyPlan depends on both: it plans the pin and leaves
// updated_at unknown.
func (f *pluginsFake) updatePlugin(w http.ResponseWriter, r *http.Request) {
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.pluginLocked(r.PathValue("plugin_id"))
	if p == nil {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "plugin not found")
		return
	}
	p["served_version_id"], p["served_version_pinned"], p["updated_at"] = body["served_version_id"], true, f.nowLocked()
	sdktest.WriteJSON(w, http.StatusOK, p)
}

// deletePlugin deletes the plugin with its versions and settings.
func (f *pluginsFake) deletePlugin(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := r.PathValue("plugin_id")
	if f.pluginLocked(id) == nil {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "plugin not found")
		return
	}
	f.dropPluginLocked(id)
	sdktest.WriteJSON(w, http.StatusOK, map[string]any{"type": "plugin_deleted", "id": id})
}

// listPlugins lists the plugins newest first, filtered by owner_type,
// owner_user_id and marketplace_id.
func (f *pluginsFake) listPlugins(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f.mu.Lock()
	items := newestFirst(f.plugins, func(p map[string]any) bool {
		owner := p["owner"].(map[string]any)
		return (!q.Has("owner_type") || owner["type"] == q.Get("owner_type")) &&
			(!q.Has("owner_user_id") || owner["user_id"] == q.Get("owner_user_id")) &&
			(!q.Has("marketplace_id") || p["marketplace_id"] == q.Get("marketplace_id"))
	})
	f.mu.Unlock()
	f.page(w, r, items)
}

// --- Versions ---

// createVersion stores a version of the plugin from the canned upload.
func (f *pluginsFake) createVersion(w http.ResponseWriter, r *http.Request) {
	fields, ok := uploadFields(w, r)
	if !ok {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.pluginLocked(r.PathValue("plugin_id"))
	if p == nil {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "plugin not found")
		return
	}
	sdktest.WriteJSON(w, http.StatusOK, f.storeVersionLocked(p, f.upload, fields["release_notes"]))
}

func (f *pluginsFake) getVersion(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	versions := f.versions[r.PathValue("plugin_id")]
	if i := slices.IndexFunc(versions, func(v map[string]any) bool { return v["id"] == r.PathValue("version_id") }); i >= 0 {
		sdktest.WriteJSON(w, http.StatusOK, versions[i])
		return
	}
	sdktest.WriteAPIError(w, http.StatusNotFound, "", "plugin version not found")
}

func (f *pluginsFake) listVersions(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	versions, ok := f.versions[r.PathValue("plugin_id")]
	items := newestFirst(versions, nil)
	f.mu.Unlock()
	if !ok {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "plugin not found")
		return
	}
	f.page(w, r, items)
}

// --- Installation settings ---

// settingTarget is a setting's target as the paths name it: the literal
// organization, or the group's ID.
func settingTarget(s map[string]any) string {
	t := s["target"].(map[string]any)
	if id, ok := t["rbac_group_id"].(string); ok {
		return id
	}
	return t["type"].(string)
}

// pluginSettings returns the settings of the organization-owned plugin the
// path names, or answers 404: "A member-owned Plugin has shares instead of
// installation settings, so this path returns 404 for one"
// (https://platform.claude.com/docs/en/api/beta/organization/plugins/installation_settings/list).
// PluginInstallationSettingResource.Read takes the 404 of a deleted plugin
// as the setting gone.
func (f *pluginsFake) pluginSettings(w http.ResponseWriter, r *http.Request) (string, bool) {
	id := r.PathValue("plugin_id")
	if _, ok := f.settings[id]; !ok {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "no organization-owned plugin "+id)
		return "", false
	}
	return id, true
}

// setLocked creates or changes the setting of target.
func (f *pluginsFake) setLocked(pluginID, target string, preference any) map[string]any {
	now := f.nowLocked()
	for _, s := range f.settings[pluginID] {
		if settingTarget(s) == target {
			s["installation_preference"], s["updated_at"] = preference, now
			return s
		}
	}
	t := map[string]any{"type": "rbac_group", "rbac_group_id": target}
	if target == "organization" {
		t = map[string]any{"type": "organization"}
	}
	s := map[string]any{
		"type": "plugin_installation_setting", "plugin_id": pluginID, "target": t,
		"installation_preference": preference, "created_at": now, "updated_at": now,
	}
	f.settings[pluginID] = append(f.settings[pluginID], s)
	return s
}

func (f *pluginsFake) setSetting(w http.ResponseWriter, r *http.Request) {
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if id, ok := f.pluginSettings(w, r); ok {
		sdktest.WriteJSON(w, http.StatusOK, f.setLocked(id, r.PathValue("target"), body["installation_preference"]))
	}
}

func (f *pluginsFake) removeSetting(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id, ok := f.pluginSettings(w, r)
	if !ok {
		return
	}
	target := r.PathValue("target")
	i := slices.IndexFunc(f.settings[id], func(s map[string]any) bool { return settingTarget(s) == target })
	if i < 0 {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "the target holds no setting of its own")
		return
	}
	s := f.settings[id][i]
	f.settings[id] = slices.Delete(f.settings[id], i, i+1)
	sdktest.WriteJSON(w, http.StatusOK, map[string]any{"type": "plugin_installation_setting_deleted", "plugin_id": id, "target": s["target"]})
}

// listSettings lists a plugin's settings newest first, filtered by
// target_type.
func (f *pluginsFake) listSettings(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	id, ok := f.pluginSettings(w, r)
	items := newestFirst(f.settings[id], byTargetType(r))
	f.mu.Unlock()
	if ok {
		f.page(w, r, items)
	}
}

func byTargetType(r *http.Request) func(map[string]any) bool {
	tt := r.URL.Query().Get("target_type")
	return func(s map[string]any) bool { return tt == "" || s["target"].(map[string]any)["type"] == tt }
}

// --- Marketplaces ---

// listMarketplaces lists the marketplaces newest first, filtered by
// owner_type and source.
func (f *pluginsFake) listMarketplaces(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f.mu.Lock()
	items := newestFirst(f.marketplaces, func(m map[string]any) bool {
		return (!q.Has("owner_type") || m["owner"].(map[string]any)["type"] == q.Get("owner_type")) &&
			(!q.Has("source") || m["source"] == q.Get("source"))
	})
	f.mu.Unlock()
	f.page(w, r, items)
}

func (f *pluginsFake) getMarketplace(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m := f.marketplaceLocked(r.PathValue("marketplace_id")); m != nil {
		sdktest.WriteJSON(w, http.StatusOK, m)
		return
	}
	sdktest.WriteAPIError(w, http.StatusNotFound, "", "marketplace not found")
}

func (f *pluginsFake) updateMarketplace(w http.ResponseWriter, r *http.Request) {
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.marketplaceLocked(r.PathValue("marketplace_id"))
	if m == nil {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "marketplace not found")
		return
	}
	m["default_installation_preference"] = body["default_installation_preference"]
	sdktest.WriteJSON(w, http.StatusOK, m)
}

// --- Lists ---

// newestFirst returns copies of the items keep selects (all of them when
// keep is nil), newest first, as every Plugins API list answers. The caller
// holds f.mu.
func newestFirst(items []map[string]any, keep func(map[string]any) bool) []map[string]any {
	var out []map[string]any
	for _, it := range slices.Backward(items) {
		if keep == nil || keep(it) {
			out = append(out, sdktest.DeepCopy(it))
		}
	}
	return out
}

// page answers one page of a list in the page/next_page family: "an opaque
// next_page cursor, passed back as page, until it is null" (guide,
// Pagination). A cursor names an item's id, or a setting's target, which has
// no id.
func (f *pluginsFake) page(w http.ResponseWriter, r *http.Request, items []map[string]any) {
	f.PaginateBy(w, r, sdktest.PageToken, f.pageSize, items, func(item map[string]any) string {
		if id, ok := item["id"].(string); ok {
			return id
		}
		return settingTarget(item)
	})
}

// --- Assertions and helpers ---

// assertRequests checks that every request carried the Admin key alone, the
// beta header and the beta=true query flag.
func (f *pluginsFake) assertRequests() {
	f.t.Helper()
	f.AssertShape(sdktest.Shape{Credential: sdktest.AdminKey, Beta: pluginsBeta, BetaQuery: true}, allRoutes...)
}

// assertWalks fails the test unless the requests to route on path (every
// path when "") that were not faulted form complete walks, each one of
// walks, and every walk occurs: each read of a list paged to its end with
// the same queries, a number of reads that belongs to terraform-plugin-testing.
func (f *pluginsFake) assertWalks(route, path string, walks ...[]string) {
	f.t.Helper()
	var got []string
	for _, r := range f.Requests(route) {
		if r.Fault == "" && (path == "" || r.Path == path) {
			got = append(got, r.Query.Encode())
		}
	}
	seen := make([]bool, len(walks))
	for i := 0; i < len(got); {
		j := slices.IndexFunc(walks, func(w []string) bool { return i+len(w) <= len(got) && slices.Equal(got[i:i+len(w)], w) })
		if j < 0 {
			f.t.Errorf("%s %s: requests from %d on do not start a complete walk:\n got: %q\nwalks: %q", route, path, i+1, got, walks)
			return
		}
		seen[j] = true
		i += len(walks[j])
	}
	for j, ok := range seen {
		if !ok {
			f.t.Errorf("%s %s: no read walked %q; got %q", route, path, walks[j], got)
		}
	}
}

// uploadedFiles returns the files[] file names and the other fields of the
// n-th request to route, a multipart upload.
func (f *pluginsFake) uploadedFiles(route string, n int) ([]string, map[string]string) {
	f.t.Helper()
	reqs := f.Requests(route)
	if len(reqs) <= n {
		f.t.Fatalf("%s: %d requests, want at least %d", route, len(reqs), n+1)
	}
	parts, err := reqs[n].Parts()
	if err != nil {
		f.t.Fatalf("%s request %d: %v", route, n+1, err)
	}
	var names []string
	fields := map[string]string{}
	for _, p := range parts {
		if p.Field == "files[]" {
			names = append(names, p.Filename)
		} else {
			fields[p.Field] = string(p.Content)
		}
	}
	return names, fields
}

// writePluginDir writes a plugin directory, its manifest and one skill, and
// returns its path.
func writePluginDir(t *testing.T, name, version string) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		".claude-plugin/plugin.json": fmt.Sprintf(`{"name": %q, "displayName": "Sales Toolkit", "description": "Account research and call prep.", "version": %q}`,
			name, version),
		"skills/account-research/SKILL.md": "---\nname: account-research\ndescription: Researches a customer account.\n---\nSteps.\n",
	}
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// writePluginArchive writes a .plugin archive wrapping the plugin in one
// top-level folder, with an MCP server in its manifest.
func writePluginArchive(t *testing.T, name, version string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "Sales-Toolkit.plugin")
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for rel, content := range map[string]string{
		"sales-toolkit/.claude-plugin/plugin.json": fmt.Sprintf(`{"name": %q, "version": %q, "mcpServers": {"crm": {"command": "crm-mcp"}}}`, name, version),
		"sales-toolkit/skills/call-prep/SKILL.md":  "---\nname: call-prep\ndescription: Prepares a sales call.\n---\n",
	} {
		fw, err := zw.Create(rel)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// hcl quotes s as an HCL string.
func hcl(s string) string { return strconv.Quote(s) }

// str is the check of an exact string, or of null for nil.
func str(v any) knownvalue.Check {
	if v == nil {
		return knownvalue.Null()
	}
	return knownvalue.StringExact(v.(string))
}
