// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package plugins_test

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"

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

	// libraryMarketplaceID is the organization's library marketplace, "the
	// organization-owned manual marketplace that uploads go to when you do
	// not name a marketplace", "created the first time something is uploaded
	// to it" (guide, Marketplaces).
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

// installationPreferences are the four values of an installation setting or
// a marketplace default.
var installationPreferences = []string{"required", "auto_install", "available", "not_available"}

// settingTarget is the {target} the installation-setting paths accept: "the
// literal organization ... or an RBAC Group's ID (prefixed rbac_group_)".
var settingTarget = regexp.MustCompile(`^(organization|rbac_group_.+)$`)

var baseTime = time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)

// pluginsFake is the Plugins API of one Claude Enterprise organization.
type pluginsFake struct {
	*sdktest.Server
	t        *testing.T
	pageSize int // largest page the fake answers below the request's limit; 0 for the limit alone

	mu           sync.Mutex
	clock        int
	seq          int
	plugins      []map[string]any            // creation order
	versions     map[string][]map[string]any // by plugin ID, creation order
	settings     map[string][]map[string]any // by plugin ID, creation order
	marketplaces []map[string]any            // creation order
	scan         map[string]any              // content_scan of the next stored versions
	pending      bool                        // the next create answers registration_pending
	cursors      map[string]int              // next_page tokens handed out, to the index of the page they open
}

func newPluginsFake(t *testing.T, pageSize int) *pluginsFake {
	t.Helper()
	f := &pluginsFake{
		Server: sdktest.NewServer(t), t: t, pageSize: pageSize,
		versions: map[string][]map[string]any{}, settings: map[string][]map[string]any{},
		scan:    map[string]any{"status": "completed", "assessment": "pass", "reason": nil},
		cursors: map[string]int{},
	}
	f.handle(routeCreatePlugin, f.createPlugin)
	f.handle(routeListPlugins, f.listPlugins)
	f.handle(routeGetPlugin, f.getPlugin)
	f.handle(routeUpdatePlugin, f.updatePlugin)
	f.handle(routeDeletePlugin, f.deletePlugin)
	f.handle(routeCreateVersion, f.createVersion)
	f.handle(routeListVersions, f.listVersions)
	f.handle(routeGetVersion, f.getVersion)
	f.handle(routeListSettings, f.listSettings)
	f.handle(routeSetSetting, f.setSetting)
	f.handle(routeRemoveSetting, f.removeSetting)
	f.handle(routeListMarketplaces, f.listMarketplaces)
	f.handle(routeGetMarketplace, f.getMarketplace)
	f.handle(routeUpdateMarketplace, f.updateMarketplace)
	return f
}

// handle registers h behind the beta header check.
func (f *pluginsFake) handle(route string, h http.HandlerFunc) {
	f.Handle(route, func(w http.ResponseWriter, r *http.Request) {
		if !slices.Contains(strings.Split(strings.ReplaceAll(r.Header.Get("anthropic-beta"), " ", ""), ","), pluginsBeta) {
			sdktest.WriteAPIError(w, http.StatusNotFound, "", "Not found")
			return
		}
		h(w, r)
	})
}

// writeError writes the error envelope with the details object the Plugins
// API adds "when one status has several causes that you would handle
// differently" (guide, Error responses).
func writeError(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	if details == nil {
		details = map[string]any{}
	}
	details["error_code"] = code
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

// --- Seeding ---

// putMarketplace stores a marketplace as the API returns it: owner
// organization (userID "") or a member, source manual or a Git host.
func (f *pluginsFake) putMarketplace(id, name, userID, source, defaultPreference string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.putMarketplaceLocked(id, name, userID, source, defaultPreference)
}

func (f *pluginsFake) putMarketplaceLocked(id, name, userID, source, defaultPreference string) {
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

// putMemberPlugin stores a plugin in the personal marketplace of userID,
// with one version.
func (f *pluginsFake) putMemberPlugin(id, name, userID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	mp := "marketplace_" + userID
	if f.marketplaceLocked(mp) == nil {
		f.putMarketplaceLocked(mp, "personal", userID, "manual", "")
	}
	p := map[string]any{
		"type": "plugin", "id": id, "name": name, "owner": map[string]any{"type": "user", "user_id": userID},
		"marketplace_id": mp, "served_version_pinned": false, "created_at": f.nowLocked(),
		"created_by": map[string]any{"type": "user_actor", "user_id": userID, "email_address": nil},
	}
	f.plugins = append(f.plugins, p)
	v := f.storeVersionLocked(p, upload{manifest: manifest{Name: name}}, "")
	v["created_by"] = p["created_by"]
}

// setScan sets the content_scan of the versions stored from now on (nil:
// not scanned).
func (f *pluginsFake) setScan(scan map[string]any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.scan = scan
}

// deletePluginOutOfBand deletes a plugin as an administrator does in
// claude.ai.
func (f *pluginsFake) deletePluginOutOfBand(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dropPluginLocked(id)
}

// --- Uploads ---

type manifest struct {
	Name        string                     `json:"name"`
	DisplayName string                     `json:"displayName"`
	Description string                     `json:"description"`
	Version     string                     `json:"version"`
	MCPServers  map[string]json.RawMessage `json:"mcpServers"`
}

// upload is what the API reads from an upload: the manifest and the skills'
// frontmatter.
type upload struct {
	manifest manifest
	skills   [][2]string // name, description
	names    []string    // the uploaded file names, as sent
}

// readUpload reads a multipart upload: "files[] is either one .zip or
// .plugin archive, or one part for each file, where each part's file name is
// the file's path within the plugin", "a part named plain files is not
// read"; it "must contain exactly one manifest, at .claude-plugin/plugin.json,
// which must declare a name"; an archive "may wrap everything in one
// top-level folder" (guide, Upload requirements). Other fields are returned.
func readUpload(r *http.Request) (upload, map[string]string, error) {
	parts, err := sdktest.ReadParts(r)
	if err != nil {
		return upload{}, nil, err
	}
	files := map[string][]byte{}
	fields := map[string]string{}
	var u upload
	for _, p := range parts {
		switch {
		case p.Field == "files[]":
			files[p.Filename] = p.Content
			u.names = append(u.names, p.Filename)
		case p.Filename == "":
			fields[p.Field] = string(p.Content)
		}
	}
	if len(files) == 1 && regexp.MustCompile(`(?i)\.(zip|plugin)$`).MatchString(u.names[0]) {
		if files, err = unzip(files[u.names[0]]); err != nil {
			return upload{}, nil, err
		}
	}
	raw, ok := files[".claude-plugin/plugin.json"]
	if !ok {
		return upload{}, nil, fmt.Errorf("the upload has no .claude-plugin/plugin.json")
	}
	if err := json.Unmarshal(raw, &u.manifest); err != nil || u.manifest.Name == "" {
		return upload{}, nil, fmt.Errorf("the manifest must declare a name")
	}
	for name, content := range files {
		if m := regexp.MustCompile(`^skills/[^/]+/SKILL\.md$`).FindString(name); m != "" {
			u.skills = append(u.skills, frontmatter(string(content)))
		}
	}
	slices.SortFunc(u.skills, func(a, b [2]string) int { return strings.Compare(a[0], b[0]) })
	return u, fields, nil
}

// unzip reads an archive's files, dropping one top-level folder that wraps
// everything.
func unzip(data []byte) (map[string][]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	files := map[string][]byte{}
	for _, zf := range zr.File {
		if zf.FileInfo().IsDir() {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			return nil, err
		}
		content, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			return nil, err
		}
		files[zf.Name] = content
	}
	if _, ok := files[".claude-plugin/plugin.json"]; !ok {
		unwrapped := map[string][]byte{}
		for name, content := range files {
			_, rest, _ := strings.Cut(name, "/")
			unwrapped[rest] = content
		}
		files = unwrapped
	}
	return files, nil
}

// frontmatter returns the name and description a SKILL.md declares.
func frontmatter(content string) [2]string {
	var out [2]string
	for line := range strings.SplitSeq(content, "\n") {
		if v, ok := strings.CutPrefix(line, "name: "); ok {
			out[0] = v
		}
		if v, ok := strings.CutPrefix(line, "description: "); ok {
			out[1] = v
		}
	}
	return out
}

// storeVersionLocked stores a version of p from u: its display fields come
// from the manifest ("manifest_version ... normalized for display", a leading
// v dropped), its components from the skills and MCP servers it declares,
// its reach is remote with an MCP server and contained otherwise. Unless p is
// pinned, the new version is served ("it becomes the version served to
// members unless the Plugin's served version has been pinned").
func (f *pluginsFake) storeVersionLocked(p map[string]any, u upload, releaseNotes string) map[string]any {
	components := []map[string]any{}
	for _, s := range u.skills {
		components = append(components, map[string]any{"type": "skill", "name": s[0], "description": s[1]})
	}
	reach := "contained"
	for _, name := range slices.Sorted(maps.Keys(u.manifest.MCPServers)) {
		components = append(components, map[string]any{"type": "mcp_server", "name": name, "description": nil})
		reach = "remote"
	}
	v := map[string]any{
		"type": "plugin_version", "id": f.newIDLocked("pluginver_"), "plugin_id": p["id"],
		"display_name": nullIfEmpty(u.manifest.DisplayName), "description": nullIfEmpty(u.manifest.Description),
		"manifest_version": nullIfEmpty(strings.TrimPrefix(u.manifest.Version, "v")), "release_notes": nullIfEmpty(releaseNotes),
		"created_by": map[string]any{"type": "api_actor", "api_key_id": uploaderKeyID}, "content_scan": f.scan,
		"components": components, "reach": reach, "created_at": f.nowLocked(),
	}
	id := p["id"].(string)
	f.versions[id] = append(f.versions[id], v)
	p["latest_version_id"] = v["id"]
	if pinned, _ := p["served_version_pinned"].(bool); !pinned {
		serveLocked(p, v)
	}
	p["updated_at"] = v["created_at"]
	return v
}

// serveLocked makes v the served version of p, whose display fields
// "describe the served version".
func serveLocked(p, v map[string]any) {
	p["served_version_id"] = v["id"]
	for _, k := range []string{"display_name", "description", "manifest_version", "content_scan", "components", "reach"} {
		p[k] = v[k]
	}
}

// snapshot deep-copies items, so a response is encoded after the lock is
// released without racing a later write.
func snapshot(items []map[string]any) []map[string]any {
	out := make([]map[string]any, len(items))
	for i, it := range items {
		raw, _ := json.Marshal(it)
		_ = json.Unmarshal(raw, &out[i])
	}
	return out
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// --- Lookups ---

func (f *pluginsFake) pluginLocked(id string) map[string]any {
	for _, p := range f.plugins {
		if p["id"] == id {
			return p
		}
	}
	return nil
}

func (f *pluginsFake) marketplaceLocked(id string) map[string]any {
	for _, m := range f.marketplaces {
		if m["id"] == id {
			return m
		}
	}
	return nil
}

func memberOwned(p map[string]any) bool { return p["owner"].(map[string]any)["type"] == "user" }

func (f *pluginsFake) dropPluginLocked(id string) {
	f.plugins = slices.DeleteFunc(f.plugins, func(p map[string]any) bool { return p["id"] == id })
	delete(f.versions, id)
	delete(f.settings, id)
}

// viewLocked is a plugin as the API answers it: an organization-owned
// plugin's organization_installation_preference is its own organization-wide
// setting, or its marketplace's default ("not_available if never set") with
// _inherited true; both are null for a member-owned plugin.
func (f *pluginsFake) viewLocked(p map[string]any) map[string]any {
	out := maps.Clone(p)
	out["organization_installation_preference"], out["organization_installation_preference_inherited"] = nil, nil
	if memberOwned(p) {
		return out
	}
	for _, s := range f.settings[p["id"].(string)] {
		if s["target"].(map[string]any)["type"] == "organization" {
			out["organization_installation_preference"], out["organization_installation_preference_inherited"] = s["installation_preference"], false
			return out
		}
	}
	def := f.marketplaceLocked(p["marketplace_id"].(string))["default_installation_preference"]
	out["organization_installation_preference"], out["organization_installation_preference_inherited"] = def, true
	return out
}

// --- Plugins ---

// createPlugin applies Create Plugin: into marketplace_id ("an
// organization-owned manual marketplace": 404 when it is not one of the
// organization's, 403 for a member's personal one, 400 for a Git one) or
// the library marketplace; 409 plugin_name_taken, with details.plugin_id,
// when the name is taken in that marketplace.
func (f *pluginsFake) createPlugin(w http.ResponseWriter, r *http.Request) {
	u, fields, err := readUpload(r)
	if err != nil {
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", err.Error())
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	mpID := fields["marketplace_id"]
	if mpID == "" {
		mpID = libraryMarketplaceID
		if f.marketplaceLocked(mpID) == nil {
			f.putMarketplaceLocked(mpID, "library", "", "manual", "")
		}
	}
	mp := f.marketplaceLocked(mpID)
	switch {
	case mp == nil:
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "marketplace_id: not a marketplace of the organization")
		return
	case mp["owner"].(map[string]any)["type"] == "user":
		sdktest.WriteAPIError(w, http.StatusForbidden, "", "marketplace_id: a member's personal marketplace")
		return
	case mp["source"] != "manual":
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", "marketplace_id: synchronized from Git")
		return
	}
	for _, p := range f.plugins {
		if p["marketplace_id"] == mpID && p["name"] == u.manifest.Name {
			writeError(w, http.StatusConflict, "plugin_name_taken", "name already taken", map[string]any{"plugin_id": p["id"]})
			return
		}
	}
	p := f.newOrgPluginLocked(u, mpID, fields["release_notes"])
	if f.pending {
		// "A 503 with error_code registration_pending means the Plugin and
		// its version were stored (their IDs are in details) but are not yet
		// usable in claude.ai: do not retry the create; create a version on
		// the stored Plugin instead, which completes it."
		f.pending = false
		writeError(w, http.StatusServiceUnavailable, "registration_pending", "the plugin's skills are not registered yet",
			map[string]any{"plugin_id": p["id"], "plugin_version_id": p["served_version_id"]})
		return
	}
	sdktest.WriteJSON(w, http.StatusOK, f.viewLocked(p))
}

// answerRegistrationPending makes the next create store the plugin but
// answer 503 registration_pending.
func (f *pluginsFake) answerRegistrationPending() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pending = true
}

// newOrgPluginLocked stores an organization-owned plugin and its first
// version, which it serves.
func (f *pluginsFake) newOrgPluginLocked(u upload, marketplaceID, releaseNotes string) map[string]any {
	p := map[string]any{
		"type": "plugin", "id": f.newIDLocked("plugin_"), "name": u.manifest.Name, "owner": map[string]any{"type": "organization"},
		"marketplace_id": marketplaceID, "served_version_pinned": false, "created_at": f.nowLocked(),
		"created_by": map[string]any{"type": "api_actor", "api_key_id": uploaderKeyID},
	}
	f.plugins = append(f.plugins, p)
	f.storeVersionLocked(p, u, releaseNotes)
	return p
}

// putOrgPlugin stores an organization-owned plugin with one version in the
// library marketplace, as an upload outside Terraform does, and returns the
// plugin's ID and the version's.
func (f *pluginsFake) putOrgPlugin(name string) (string, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.marketplaceLocked(libraryMarketplaceID) == nil {
		f.putMarketplaceLocked(libraryMarketplaceID, "library", "", "manual", "")
	}
	p := f.newOrgPluginLocked(upload{manifest: manifest{Name: name, DisplayName: "Seeded", Version: "1.0.0"}}, libraryMarketplaceID, "")
	return p["id"].(string), p["served_version_id"].(string)
}

// uploadVersionOutOfBand stores a new version of a plugin, as an upload in
// claude.ai does, and returns its ID.
func (f *pluginsFake) uploadVersionOutOfBand(pluginID, version string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.pluginLocked(pluginID)
	v := f.storeVersionLocked(p, upload{manifest: manifest{Name: p["name"].(string), DisplayName: "Sales Toolkit", Version: version}}, "")
	return v["id"].(string)
}

// plugin returns a copy of a stored plugin as the API answers it, or nil.
func (f *pluginsFake) plugin(id string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.pluginLocked(id)
	if p == nil {
		return nil
	}
	return snapshot([]map[string]any{f.viewLocked(p)})[0]
}

// versionCount returns the number of versions a plugin holds.
func (f *pluginsFake) versionCount(id string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.versions[id])
}

func (f *pluginsFake) getPlugin(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.pluginLocked(r.PathValue("plugin_id"))
	if p == nil {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "plugin not found")
		return
	}
	sdktest.WriteJSON(w, http.StatusOK, f.viewLocked(p))
}

// updatePlugin applies Update Plugin: serve served_version_id and pin
// ("updated_at ... moves on a served-version change, including pinning an
// unpinned Plugin to the version it already serves"); a scan still running
// is a 409 scan_pending, an errored scan or a fail or unknown verdict a 400
// scan_failed; a member-owned plugin is a 403.
func (f *pluginsFake) updatePlugin(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ServedVersionID string `json:"served_version_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", "body: "+err.Error())
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.pluginLocked(r.PathValue("plugin_id"))
	if p == nil {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "plugin not found")
		return
	}
	if memberOwned(p) {
		sdktest.WriteAPIError(w, http.StatusForbidden, "", "a member-owned plugin cannot be updated")
		return
	}
	var v map[string]any
	for _, c := range f.versions[p["id"].(string)] {
		if c["id"] == body.ServedVersionID {
			v = c
		}
	}
	if v == nil {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "served_version_id: no such version of the plugin")
		return
	}
	if scan, _ := v["content_scan"].(map[string]any); scan != nil {
		switch {
		case scan["status"] == "processing":
			writeError(w, http.StatusConflict, "scan_pending", "the version's content scan is still running", map[string]any{"plugin_version_id": v["id"]})
			return
		case scan["status"] == "errored", scan["assessment"] == "fail", scan["assessment"] == "unknown":
			writeError(w, http.StatusBadRequest, "scan_failed", "the version's content scan failed", map[string]any{"plugin_version_id": v["id"]})
			return
		}
	}
	serveLocked(p, v)
	p["served_version_pinned"] = true
	p["updated_at"] = f.nowLocked()
	sdktest.WriteJSON(w, http.StatusOK, f.viewLocked(p))
}

// deletePlugin applies Delete Plugin: the plugin, its versions and its
// installation settings go; a plugin synchronized from a repository is a 400.
func (f *pluginsFake) deletePlugin(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	id := r.PathValue("plugin_id")
	p := f.pluginLocked(id)
	if p == nil {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "plugin not found")
		return
	}
	if f.marketplaceLocked(p["marketplace_id"].(string))["source"] != "manual" {
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", "a plugin synchronized from a repository is removed from the repository")
		return
	}
	f.dropPluginLocked(id)
	sdktest.WriteJSON(w, http.StatusOK, map[string]any{"type": "plugin_deleted", "id": id})
}

// listPlugins applies List Plugins: newest first, owner_type, owner_user_id
// and marketplace_id filters, limit 1..100.
func (f *pluginsFake) listPlugins(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f.mu.Lock()
	var items []map[string]any
	for _, p := range slices.Backward(f.plugins) {
		owner := p["owner"].(map[string]any)
		if (q.Has("owner_type") && owner["type"] != q.Get("owner_type")) ||
			(q.Has("owner_user_id") && owner["user_id"] != q.Get("owner_user_id")) ||
			(q.Has("marketplace_id") && p["marketplace_id"] != q.Get("marketplace_id")) {
			continue
		}
		items = append(items, f.viewLocked(p))
	}
	items = snapshot(items)
	f.mu.Unlock()
	f.page(w, r, 100, items)
}

// --- Versions ---

// createVersion applies Create Plugin Version: "The uploaded manifest's name
// must equal the Plugin's name"; "Only a Plugin in a manual marketplace takes
// uploads".
func (f *pluginsFake) createVersion(w http.ResponseWriter, r *http.Request) {
	u, fields, err := readUpload(r)
	if err != nil {
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", err.Error())
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.pluginLocked(r.PathValue("plugin_id"))
	switch {
	case p == nil:
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "plugin not found")
		return
	case memberOwned(p):
		sdktest.WriteAPIError(w, http.StatusForbidden, "", "a member-owned plugin takes no upload here")
		return
	case u.manifest.Name != p["name"]:
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", fmt.Sprintf("the manifest's name %q is not the plugin's %q", u.manifest.Name, p["name"]))
		return
	}
	sdktest.WriteJSON(w, http.StatusOK, f.storeVersionLocked(p, u, fields["release_notes"]))
}

func (f *pluginsFake) getVersion(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, v := range f.versions[r.PathValue("plugin_id")] {
		if v["id"] == r.PathValue("version_id") {
			sdktest.WriteJSON(w, http.StatusOK, v)
			return
		}
	}
	sdktest.WriteAPIError(w, http.StatusNotFound, "", "plugin version not found")
}

// listVersions applies List Plugin Versions: newest first, limit 1..1000.
func (f *pluginsFake) listVersions(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	if f.pluginLocked(r.PathValue("plugin_id")) == nil {
		f.mu.Unlock()
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "plugin not found")
		return
	}
	items := snapshot(f.versions[r.PathValue("plugin_id")])
	f.mu.Unlock()
	slices.Reverse(items)
	f.page(w, r, 1000, items)
}

// --- Installation settings ---

func settingTargetObject(target string) map[string]any {
	if target == "organization" {
		return map[string]any{"type": "organization"}
	}
	return map[string]any{"type": "rbac_group", "rbac_group_id": target}
}

func settingTargetSegment(s map[string]any) string {
	t := s["target"].(map[string]any)
	if t["type"] == "rbac_group" {
		return t["rbac_group_id"].(string)
	}
	return "organization"
}

// orgPluginLocked is the organization-owned plugin the path names, or
// answers 404: "A member-owned Plugin has shares instead of installation
// settings, so this path returns 404 for one" (installation_settings pages).
func (f *pluginsFake) orgPluginLocked(w http.ResponseWriter, r *http.Request) map[string]any {
	p := f.pluginLocked(r.PathValue("plugin_id"))
	if p == nil || memberOwned(p) {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "no organization-owned plugin "+r.PathValue("plugin_id"))
		return nil
	}
	return p
}

// setSetting applies Set Plugin Installation Setting: an upsert; "Writing
// the value a target already holds of its own changes nothing".
func (f *pluginsFake) setSetting(w http.ResponseWriter, r *http.Request) {
	var body struct {
		InstallationPreference string `json:"installation_preference"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	target := r.PathValue("target")
	if !settingTarget.MatchString(target) || !slices.Contains(installationPreferences, body.InstallationPreference) {
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", "target or installation_preference: invalid")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.orgPluginLocked(w, r)
	if p == nil {
		return
	}
	id := p["id"].(string)
	for _, s := range f.settings[id] {
		if settingTargetSegment(s) == target {
			if s["installation_preference"] != body.InstallationPreference {
				s["installation_preference"], s["updated_at"] = body.InstallationPreference, f.nowLocked()
			}
			sdktest.WriteJSON(w, http.StatusOK, s)
			return
		}
	}
	now := f.nowLocked()
	s := map[string]any{
		"type": "plugin_installation_setting", "plugin_id": id, "target": settingTargetObject(target),
		"installation_preference": body.InstallationPreference, "created_at": now, "updated_at": now,
	}
	f.settings[id] = append(f.settings[id], s)
	sdktest.WriteJSON(w, http.StatusOK, s)
}

// putSetting stores a setting written outside Terraform.
func (f *pluginsFake) putSetting(pluginID, target, preference string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.nowLocked()
	f.settings[pluginID] = append(f.settings[pluginID], map[string]any{
		"type": "plugin_installation_setting", "plugin_id": pluginID, "target": settingTargetObject(target),
		"installation_preference": preference, "created_at": now, "updated_at": now,
	})
}

// removeSettingOutOfBand removes a setting as an administrator does in
// claude.ai.
func (f *pluginsFake) removeSettingOutOfBand(pluginID, target string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.settings[pluginID] = slices.DeleteFunc(f.settings[pluginID], func(s map[string]any) bool { return settingTargetSegment(s) == target })
}

// setting returns the setting a plugin holds for target, or nil.
func (f *pluginsFake) setting(pluginID, target string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, s := range f.settings[pluginID] {
		if settingTargetSegment(s) == target {
			return s
		}
	}
	return nil
}

// removeSetting applies Remove Plugin Installation Setting: "A target that
// holds no setting of its own returns 404".
func (f *pluginsFake) removeSetting(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	p := f.orgPluginLocked(w, r)
	if p == nil {
		return
	}
	id, target := p["id"].(string), r.PathValue("target")
	n := len(f.settings[id])
	f.settings[id] = slices.DeleteFunc(f.settings[id], func(s map[string]any) bool { return settingTargetSegment(s) == target })
	if len(f.settings[id]) == n {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "the target holds no setting of its own")
		return
	}
	sdktest.WriteJSON(w, http.StatusOK, map[string]any{"type": "plugin_installation_setting_deleted", "plugin_id": id, "target": settingTargetObject(target)})
}

// listSettings applies List Plugin Installation Settings: "most recently
// created first", target_type filter, limit 1..100.
func (f *pluginsFake) listSettings(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	p := f.orgPluginLocked(w, r)
	if p == nil {
		f.mu.Unlock()
		return
	}
	var items []map[string]any
	for _, s := range slices.Backward(f.settings[p["id"].(string)]) {
		if tt := r.URL.Query().Get("target_type"); tt == "" || s["target"].(map[string]any)["type"] == tt {
			items = append(items, s)
		}
	}
	items = snapshot(items)
	f.mu.Unlock()
	f.page(w, r, 100, items)
}

// --- Marketplaces ---

// listMarketplaces applies List Plugin Marketplaces: newest first, owner_type
// and source filters, limit 1..1000.
func (f *pluginsFake) listMarketplaces(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	f.mu.Lock()
	var items []map[string]any
	for _, m := range slices.Backward(f.marketplaces) {
		if (q.Has("owner_type") && m["owner"].(map[string]any)["type"] != q.Get("owner_type")) ||
			(q.Has("source") && m["source"] != q.Get("source")) {
			continue
		}
		items = append(items, m)
	}
	items = snapshot(items)
	f.mu.Unlock()
	f.page(w, r, 1000, items)
}

func (f *pluginsFake) getMarketplace(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.marketplaceLocked(r.PathValue("marketplace_id"))
	if m == nil {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "marketplace not found")
		return
	}
	sdktest.WriteJSON(w, http.StatusOK, m)
}

// updateMarketplace applies Update Plugin Marketplace: "A member's personal
// marketplace cannot be updated here (403)".
func (f *pluginsFake) updateMarketplace(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DefaultInstallationPreference string `json:"default_installation_preference"`
	}
	_ = json.NewDecoder(r.Body).Decode(&body)
	if !slices.Contains(installationPreferences, body.DefaultInstallationPreference) {
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", "default_installation_preference: invalid")
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	m := f.marketplaceLocked(r.PathValue("marketplace_id"))
	switch {
	case m == nil:
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "marketplace not found")
	case m["owner"].(map[string]any)["type"] == "user":
		sdktest.WriteAPIError(w, http.StatusForbidden, "", "a member's personal marketplace cannot be updated")
	default:
		m["default_installation_preference"] = body.DefaultInstallationPreference
		sdktest.WriteJSON(w, http.StatusOK, m)
	}
}

// marketplaceDefault returns a marketplace's default_installation_preference.
func (f *pluginsFake) marketplaceDefault(id string) any {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.marketplaceLocked(id)["default_installation_preference"]
}

// deleteMarketplaceOutOfBand removes a marketplace as claude.ai does.
func (f *pluginsFake) deleteMarketplaceOutOfBand(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.marketplaces = slices.DeleteFunc(f.marketplaces, func(m map[string]any) bool { return m["id"] == id })
}

// --- Pagination ---

// page answers one page of a Plugins API list: data and next_page, "an
// opaque next_page cursor, passed back as page, until it is null" (guide,
// Pagination; every list page's "next_page: string or null"). A page holds
// at most limit items (default 20) and at most pageSize. A limit outside
// 1..maxLimit, or a page token the fake never handed out on this route or
// that was already used, is a 400 and fails the test.
func (f *pluginsFake) page(w http.ResponseWriter, r *http.Request, maxLimit int, items []map[string]any) {
	q := r.URL.Query()
	limit := 20
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > maxLimit {
			f.t.Errorf("%s: limit=%q is outside 1..%d", r.Pattern, v, maxLimit)
			sdktest.WriteAPIError(w, http.StatusBadRequest, "", "limit: out of range")
			return
		}
		limit = n
	}
	if f.pageSize > 0 && f.pageSize < limit {
		limit = f.pageSize
	}
	start := 0
	if q.Has("page") {
		key := r.URL.Path + " " + q.Get("page")
		f.mu.Lock()
		i, ok := f.cursors[key]
		delete(f.cursors, key)
		f.mu.Unlock()
		if !ok {
			f.t.Errorf("%s: page=%q was never handed out, or was sent twice", r.URL.Path, q.Get("page"))
			sdktest.WriteAPIError(w, http.StatusBadRequest, "", "page: invalid cursor")
			return
		}
		start = min(i, len(items))
	}
	end := min(start+limit, len(items))
	data := items[start:end]
	if data == nil {
		data = []map[string]any{}
	}
	var next any
	if end < len(items) {
		token := pageToken(end)
		f.mu.Lock()
		f.cursors[r.URL.Path+" "+token] = end
		f.mu.Unlock()
		next = token
	}
	sdktest.WriteJSON(w, http.StatusOK, map[string]any{"data": data, "next_page": next})
}

// pageToken is the next_page the fake hands out for the page that starts at
// item index i.
func pageToken(i int) string { return sdktest.NextPageToken(strconv.Itoa(i)) }

// --- Assertions and helpers ---

// assertRequests checks that every route the fake received carried the
// Admin key alone, the beta header and the beta=true query flag.
func (f *pluginsFake) assertRequests() {
	f.t.Helper()
	n := 0
	for _, route := range allRoutes {
		reqs := f.Requests(route)
		if len(reqs) == 0 {
			continue
		}
		n++
		f.AssertCredential(route, sdktest.AdminKey)
		f.AssertBeta(route, pluginsBeta)
		for i, r := range reqs {
			if r.Query.Get("beta") != "true" {
				f.t.Errorf("request %d to %s: query beta = %q, want true", i+1, route, r.Query.Get("beta"))
			}
		}
	}
	if n == 0 {
		f.t.Fatal("the fake received no request")
	}
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

// expectAction checks the planned action on addr, then an empty plan after
// the apply and refresh.
func expectAction(addr string, action plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{
		PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, action)},
		PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
	}
}
