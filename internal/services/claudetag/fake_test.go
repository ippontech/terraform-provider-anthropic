// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package claudetag_test

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"

	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// Routes of the Claude Tag API: the 14 endpoints of the guide,
// https://platform.claude.com/docs/en/manage-claude/claude-tag-api#endpoints.
// Every behaviour below cites the section of that guide it follows (#anchor).
const (
	routeSetUpChannel    = "POST /v1/organizations/claude_tag/slack_channels"
	routeListChannels    = "GET /v1/organizations/claude_tag/slack_channels"
	routeGetChannel      = "GET /v1/organizations/claude_tag/slack_channels/{id}"
	routeUpdateChannel   = "POST /v1/organizations/claude_tag/slack_channels/{id}"
	routeDeleteChannel   = "DELETE /v1/organizations/claude_tag/slack_channels/{id}"
	routeListWorkspaces  = "GET /v1/organizations/claude_tag/slack_workspaces"
	routeGetWorkspace    = "GET /v1/organizations/claude_tag/slack_workspaces/{id}"
	routeUpdateWorkspace = "POST /v1/organizations/claude_tag/slack_workspaces/{id}"
	routeListBundles     = "GET /v1/organizations/claude_tag/bundles"
	routeGetBundle       = "GET /v1/organizations/claude_tag/bundles/{bundle_id}"
	routeListTargets     = "GET /v1/organizations/claude_tag/bundles/{bundle_id}/targets"
	routeAttach          = "POST /v1/organizations/claude_tag/bundles/{bundle_id}/targets"
	routeGetTarget       = "GET /v1/organizations/claude_tag/bundles/{bundle_id}/targets/{target_id}"
	routeDetach          = "DELETE /v1/organizations/claude_tag/bundles/{bundle_id}/targets/{target_id}"

	// claudeTagBeta: "A request without it returns 404, exactly as if the
	// endpoint did not exist" (#endpoints, the beta note above it).
	claudeTagBeta = "experimental-claude-tag-2026-10-21"
)

var allRoutes = []string{
	routeSetUpChannel, routeListChannels, routeGetChannel, routeUpdateChannel, routeDeleteChannel,
	routeListWorkspaces, routeGetWorkspace, routeUpdateWorkspace,
	routeListBundles, routeGetBundle, routeListTargets, routeAttach, routeGetTarget, routeDetach,
}

var settingKeys = []string{"enabled", "instructions", "model", "environment_id", "guest_policy"}

var (
	// #ids: a channel ID starts with C or G; any Slack ID has only capital
	// letters and digits after its first letter and at most 32 characters.
	fakeSlackChannelID = regexp.MustCompile(`^[CG][A-Z0-9]{1,31}$`)
	fakeSlackID        = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,31}$`)
	baseTime           = time.Date(2026, 10, 19, 16, 0, 0, 0, time.UTC)
)

// level is a set-up channel or workspace: what Claude Tag stores for it.
type level struct {
	kind, id, slackWorkspace, slackChannel string
	settings                               map[string]any // the five keys; nil = not set
	createdAt, updatedAt                   string
	groupDM                                bool
}

type target struct{ id, bundleID, kind, ref, createdAt string }

// claudeTagFake is the Claude Tag API of one Claude Enterprise organization.
type claudeTagFake struct {
	*sdktest.Server
	t        *testing.T
	pageSize int

	mu             sync.Mutex
	clock, seq     int
	defaults       map[string]any   // the organization's Slack defaults (#stored-and-inherited-values)
	owned          map[string]bool  // Slack workspaces with Claude's app, owned by the organization (#set-up-a-channel)
	levels         []*level         // set-up channels and workspaces, creation order
	bundles        []map[string]any // creation order
	targets        []*target        // creation order
	environments   map[string]bool  // active environments of the organization (#settings)
	readOnlyMode   bool             // API / Terraform read-only mode (#read-only-mode)
	writesDisabled bool             // claude_tag_writes_not_enabled (#error-responses)
	refusals       map[string][]refusal
}

// refusal is an answer with an error_code that the next request to a route
// gets, for the causes the fake does not model (#error-responses).
type refusal struct {
	status int
	code   string
}

func newClaudeTagFake(t *testing.T, pageSize int) *claudeTagFake {
	t.Helper()
	f := &claudeTagFake{
		Server: sdktest.NewServer(t), t: t, pageSize: pageSize,
		defaults: map[string]any{}, owned: map[string]bool{}, environments: map[string]bool{}, refusals: map[string][]refusal{},
	}
	f.handle(routeSetUpChannel, f.setUpChannel)
	f.handle(routeListChannels, f.listChannels)
	f.handle(routeGetChannel, f.get("slack_channel", "slch_"))
	f.handle(routeUpdateChannel, f.update("slack_channel", "slch_"))
	f.handle(routeDeleteChannel, f.deleteChannel)
	f.handle(routeListWorkspaces, f.listWorkspaces)
	f.handle(routeGetWorkspace, f.get("slack_workspace", "slws_"))
	f.handle(routeUpdateWorkspace, f.update("slack_workspace", "slws_"))
	f.handle(routeListBundles, f.listBundles)
	f.handle(routeGetBundle, f.getBundle)
	f.handle(routeListTargets, f.listTargets)
	f.handle(routeAttach, f.attach)
	f.handle(routeGetTarget, f.getTarget)
	f.handle(routeDetach, f.detach)
	return f
}

// handle registers h behind the checks every endpoint makes: the beta
// header (404 without it, #endpoints), include_inherited a Boolean, and
// absent from a write (#stored-and-inherited-values), and the organization
// enabled for changes (#error-responses, claude_tag_writes_not_enabled).
func (f *claudeTagFake) handle(route string, h http.HandlerFunc) {
	f.Handle(route, func(w http.ResponseWriter, r *http.Request) {
		if !slices.Contains(strings.Split(strings.ReplaceAll(r.Header.Get("anthropic-beta"), " ", ""), ","), claudeTagBeta) {
			sdktest.WriteAPIError(w, http.StatusNotFound, "", "Not found")
			return
		}
		inherited, has := r.URL.Query()["include_inherited"]
		switch {
		case has && r.Method != http.MethodGet:
			writeError(w, http.StatusBadRequest, "", "include_inherited is not accepted on a change", nil)
			return
		case has && inherited[0] != "true" && inherited[0] != "false":
			writeError(w, http.StatusBadRequest, "", "include_inherited must be a Boolean", nil)
			return
		}
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.Method != http.MethodGet && f.writesDisabled {
			writeError(w, http.StatusForbidden, "claude_tag_writes_not_enabled", "Your organization is not enabled for changes", nil)
			return
		}
		if queue := f.refusals[route]; len(queue) > 0 {
			f.refusals[route] = queue[1:]
			writeError(w, queue[0].status, queue[0].code, "refused: "+queue[0].code, nil)
			return
		}
		h(w, r)
	})
}

// writeError writes the error envelope, with error.details.error_code when
// one status has several causes (#error-responses).
func writeError(w http.ResponseWriter, status int, code, message string, details map[string]any) {
	e := map[string]any{"type": sdktest.ErrorType(status), "message": message}
	if code != "" {
		if details == nil {
			details = map[string]any{}
		}
		details["error_code"] = code
		e["details"] = details
	}
	sdktest.WriteJSON(w, status, map[string]any{"type": "error", "error": e, "request_id": w.Header().Get("request-id")})
}

func (f *claudeTagFake) nowLocked() string {
	f.clock++
	return baseTime.Add(time.Duration(f.clock) * time.Minute).Format(time.RFC3339)
}

func (f *claudeTagFake) newIDLocked(prefix string) string {
	f.seq++
	return fmt.Sprintf("%s%04d", prefix, f.seq)
}

// --- Seeding and out-of-band changes ---

// putWorkspace connects a Slack workspace, set up with the given settings,
// and returns its slws_ ID.
func (f *claudeTagFake) putWorkspace(slackID string, settings map[string]any) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.owned[slackID] = true
	return f.putLevelLocked("slack_workspace", slackID, "", settings).id
}

// own makes a Slack workspace the organization's without setting it up, as a
// Grid's workspace is until it is used (#set-up-channels-and-workspaces).
func (f *claudeTagFake) own(slackID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.owned[slackID] = true
}

// putChannel sets slackChan up with the given settings, as claude.ai does,
// and returns its slch_ ID.
func (f *claudeTagFake) putChannel(settings map[string]any) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.putLevelLocked("slack_channel", slackTeam, slackChan, settings).id
}

func (f *claudeTagFake) putLevelLocked(kind, slackWorkspace, slackChannel string, settings map[string]any) *level {
	prefix := map[string]string{"slack_channel": "slch_", "slack_workspace": "slws_"}[kind]
	now := f.nowLocked()
	l := &level{kind: kind, id: f.newIDLocked(prefix), slackWorkspace: slackWorkspace, slackChannel: slackChannel,
		settings: map[string]any{}, createdAt: now, updatedAt: now}
	maps.Copy(l.settings, settings)
	f.levels = append(f.levels, l)
	return l
}

func (f *claudeTagFake) putBundle(id, name string, description any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := f.nowLocked()
	f.bundles = append(f.bundles, map[string]any{
		"type": "bundle", "id": id, "display_name": name, "description": description, "created_at": now, "updated_at": now,
	})
}

func (f *claudeTagFake) putEnvironment(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.environments[id] = true
}

// personSets is a person changing a setting in claude.ai. While read-only
// mode is on, claude.ai refuses it (#read-only-mode); it reports whether the
// change landed.
func (f *claudeTagFake) personSets(id, key string, value any) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.readOnlyMode {
		return false
	}
	l := f.findLocked("", id)
	l.settings[key] = value
	l.updatedAt = f.nowLocked()
	return true
}

// removeOutOfBand drops a channel or workspace from Claude Tag: a channel
// deleted in Slack, a workspace disconnected (#read-only-mode, last
// paragraph). Its Bundle targets go with it (#remove-a-channel).
func (f *claudeTagFake) removeOutOfBand(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removeLocked(id)
}

func (f *claudeTagFake) removeLocked(id string) {
	f.levels = slices.DeleteFunc(f.levels, func(l *level) bool { return l.id == id })
	f.targets = slices.DeleteFunc(f.targets, func(t *target) bool { return t.ref == id })
}

// refuseNext makes the next request to route answer status with code.
func (f *claudeTagFake) refuseNext(route string, status int, code string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refusals[route] = append(f.refusals[route], refusal{status, code})
}

func (f *claudeTagFake) set(fn func(*claudeTagFake)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	fn(f)
}

// settingsOf returns a copy of what a channel or workspace stores, nil when
// it is not set up.
func (f *claudeTagFake) settingsOf(id string) map[string]any {
	f.mu.Lock()
	defer f.mu.Unlock()
	l := f.findLocked("", id)
	if l == nil {
		return nil
	}
	return maps.Clone(l.settings)
}

func (f *claudeTagFake) findLocked(kind, id string) *level {
	for _, l := range f.levels {
		if l.id == id && (kind == "" || l.kind == kind) {
			return l
		}
	}
	return nil
}

func (f *claudeTagFake) findSlackLocked(kind, slackWorkspace, slackChannel string) *level {
	for _, l := range f.levels {
		if l.kind == kind && l.slackWorkspace == slackWorkspace && l.slackChannel == slackChannel {
			return l
		}
	}
	return nil
}

// --- Assertions ---

// assertRequests checks that every request carried the Admin key alone in
// x-api-key, the beta header and anthropic-version 2023-06-01 (#prerequisites).
func (f *claudeTagFake) assertRequests() {
	f.t.Helper()
	n := 0
	for _, route := range allRoutes {
		reqs := f.Requests(route)
		if len(reqs) == 0 {
			continue
		}
		n++
		f.AssertCredential(route, sdktest.AdminKey)
		f.AssertBeta(route, claudeTagBeta)
		for i, r := range reqs {
			if v := r.Header.Get("anthropic-version"); v != "2023-06-01" {
				f.t.Errorf("request %d to %s: anthropic-version %q, want 2023-06-01", i+1, route, v)
			}
		}
	}
	if n == 0 {
		f.t.Fatal("the fake received no request")
	}
}

// assertLookupsAndProbes fails the test unless the requests to route are
// lookups (query lookup) and probes ("limit=1", the request that names
// nothing and confirms a 404, #error-responses): lookups of them, and at
// least one probe. How many refreshes meet the 404 belongs to
// terraform-plugin-testing.
func (f *claudeTagFake) assertLookupsAndProbes(route, lookup string, lookups int) {
	f.t.Helper()
	var gotLookups, probes int
	for _, r := range f.Requests(route) {
		switch r.Query.Encode() {
		case lookup:
			gotLookups++
		case "limit=1":
			probes++
		default:
			f.t.Errorf("%s: unexpected query %q", route, r.Query.Encode())
		}
	}
	if gotLookups != lookups || probes == 0 {
		f.t.Errorf("%s: %d lookups and %d probes, want %d lookups and at least one probe", route, gotLookups, probes, lookups)
	}
}

// pageToken is the next_page the fake hands out after item lastID.
func pageToken(lastID string) string { return sdktest.NextPageToken(lastID) }

func expectAction(addr string, action plancheck.ResourceActionType) resource.ConfigPlanChecks {
	return resource.ConfigPlanChecks{
		PreApply:             []plancheck.PlanCheck{plancheck.ExpectResourceAction(addr, action)},
		PostApplyPostRefresh: []plancheck.PlanCheck{plancheck.ExpectEmptyPlan()},
	}
}

// --- Handlers: channels and workspaces ---

// setUpChannel: "sets a Slack channel up and returns it ... If the channel is
// already set up, the request returns the existing channel and applies
// settings; the response is 200 in both cases" (#set-up-a-channel). A
// workspace without the app is a 404; the first channel set up in a workspace
// sets the workspace up (#set-up-channels-and-workspaces); when settings are
// refused after a new setup, the channel stays set up and the error carries
// slack_channel_set_up_* and its ID.
func (f *claudeTagFake) setUpChannel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Slack struct {
			WorkspaceID string `json:"workspace_id"`
			ChannelID   string `json:"channel_id"`
		} `json:"slack"`
		Settings map[string]any `json:"settings"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "", err.Error(), nil)
		return
	}
	ws, ch := body.Slack.WorkspaceID, body.Slack.ChannelID
	if !fakeSlackID.MatchString(ws) || !fakeSlackChannelID.MatchString(ch) {
		writeError(w, http.StatusBadRequest, "", "invalid Slack ID", nil)
		return
	}
	if !f.owned[ws] {
		writeError(w, http.StatusNotFound, "", "Slack workspace not found", nil)
		return
	}
	if f.findSlackLocked("slack_workspace", ws, "") == nil {
		f.putLevelLocked("slack_workspace", ws, "", nil)
	}
	l := f.findSlackLocked("slack_channel", ws, ch)
	created := l == nil
	if created {
		l = f.putLevelLocked("slack_channel", ws, ch, nil)
	}
	if body.Settings != nil {
		status, code, msg := f.applyLocked(l, body.Settings)
		f.dateLocked(l, created)
		if status != 0 {
			if created {
				code = map[int]string{400: "slack_channel_set_up_settings_refused", 404: "slack_channel_set_up_environment_not_found"}[status]
				writeError(w, status, code, msg, map[string]any{"slack_channel_id": l.id})
				return
			}
			writeError(w, status, code, msg, nil)
			return
		}
	}
	sdktest.WriteJSON(w, http.StatusOK, f.levelJSONLocked(l, false))
}

// update changes a channel's or workspace's settings: settings is required
// (#update-a-channel, #update-a-workspace); a group DM's settings cannot be
// changed (#group-direct-messages).
func (f *claudeTagFake) update(kind, prefix string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		l, ok := f.levelOfPathLocked(w, r, kind, prefix)
		if !ok {
			return
		}
		var body struct {
			Settings map[string]any `json:"settings"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Settings == nil {
			writeError(w, http.StatusBadRequest, "", "settings is required", nil)
			return
		}
		if l.groupDM {
			writeError(w, http.StatusBadRequest, "", "a group direct message's settings cannot be changed", nil)
			return
		}
		if status, code, msg := f.applyLocked(l, body.Settings); status != 0 {
			writeError(w, status, code, msg, nil)
			return
		}
		f.dateLocked(l, false)
		sdktest.WriteJSON(w, http.StatusOK, f.levelJSONLocked(l, false))
	}
}

// dateLocked dates a change applyLocked marked: a channel set up and given
// its settings in one request keeps updated_at equal to created_at.
func (f *claudeTagFake) dateLocked(l *level, created bool) {
	switch {
	case created:
		l.updatedAt = l.createdAt
	case l.updatedAt == "changed":
		l.updatedAt = f.nowLocked()
	}
}

func (f *claudeTagFake) get(kind, prefix string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if l, ok := f.levelOfPathLocked(w, r, kind, prefix); ok {
			sdktest.WriteJSON(w, http.StatusOK, f.levelJSONLocked(l, r.URL.Query().Get("include_inherited") == "true"))
		}
	}
}

// levelOfPathLocked finds the {id} of the path: "An id that is not a valid
// slch_ identifier returns 400. A valid one that names no channel of your
// organization returns 404" (#slack-channels; #slack-workspaces).
func (f *claudeTagFake) levelOfPathLocked(w http.ResponseWriter, r *http.Request, kind, prefix string) (*level, bool) {
	id := r.PathValue("id")
	if !strings.HasPrefix(id, prefix) {
		writeError(w, http.StatusBadRequest, "", "invalid "+prefix+" identifier", nil)
		return nil, false
	}
	l := f.findLocked(kind, id)
	if l == nil {
		writeError(w, http.StatusNotFound, "", kind+" not found", nil)
		return nil, false
	}
	return l, true
}

// applyLocked applies a settings change under #changing-settings: a setting
// left out is unchanged, null clears it, nothing is stored when one value is
// refused, and a change that alters nothing writes nothing. Values follow
// #settings: instructions trimmed (blank clears, at most 8,192 characters),
// model at most 128 characters, an active env_ environment of the
// organization (404 environment_not_found otherwise), no change to or from a
// ccpool_ runner pool, guest_policy one of three types. It returns a zero
// status, or the refusal; a change marks updated_at, which the caller dates.
func (f *claudeTagFake) applyLocked(l *level, patch map[string]any) (int, string, string) {
	next := maps.Clone(l.settings)
	for key, v := range patch {
		value, status, code, msg := f.checkSettingLocked(l, key, v)
		if status != 0 {
			return status, code, msg
		}
		next[key] = value
	}
	if !jsonEqual(next, l.settings) {
		l.settings = next
		l.updatedAt = "changed"
	}
	return 0, "", ""
}

func (f *claudeTagFake) checkSettingLocked(l *level, key string, v any) (any, int, string, string) {
	if pool, _ := l.settings["environment_id"].(string); key == "environment_id" && strings.HasPrefix(pool, "ccpool_") && v != pool {
		return nil, http.StatusBadRequest, "", "environment_id stores a runner pool, which this API cannot change"
	}
	if v == nil {
		return nil, 0, "", ""
	}
	switch key {
	case "enabled":
		if _, ok := v.(bool); ok {
			return v, 0, "", ""
		}
	case "instructions":
		if s, ok := v.(string); ok {
			s = strings.TrimSpace(s)
			if len([]rune(s)) > 8192 {
				return nil, http.StatusBadRequest, "", "instructions is longer than 8192 characters"
			}
			if s == "" {
				return nil, 0, "", ""
			}
			return s, 0, "", ""
		}
	case "model":
		if s, ok := v.(string); ok && s != "" && len(s) <= 128 {
			return v, 0, "", ""
		}
	case "environment_id":
		return f.checkEnvironmentLocked(v)
	case "guest_policy":
		if p, ok := v.(map[string]any); ok && slices.Contains([]any{"allow", "restrict", "channel_only"}, p["type"]) {
			return map[string]any{"type": p["type"]}, 0, "", ""
		}
	}
	return nil, http.StatusBadRequest, "", "invalid value for " + key
}

func (f *claudeTagFake) checkEnvironmentLocked(v any) (any, int, string, string) {
	s, _ := v.(string)
	switch {
	case strings.HasPrefix(s, "ccpool_"):
		return nil, http.StatusBadRequest, "", "this API cannot set a runner pool"
	case !f.environments[s]:
		return nil, http.StatusNotFound, "environment_not_found", "environment not found"
	}
	return s, 0, "", ""
}

// deleteChannel: "A channel can be removed only while Claude is off in it"
// (400 slack_channel_enabled); a group DM, and a channel holding
// configuration the API does not manage "such as a runner pool", cannot be
// removed (400, no error_code, checked first); the Bundle targets go with
// the channel, and "a repeated removal returns 404" (#remove-a-channel).
func (f *claudeTagFake) deleteChannel(w http.ResponseWriter, r *http.Request) {
	l, ok := f.levelOfPathLocked(w, r, "slack_channel", "slch_")
	if !ok {
		return
	}
	if pool, _ := l.settings["environment_id"].(string); l.groupDM || strings.HasPrefix(pool, "ccpool_") {
		writeError(w, http.StatusBadRequest, "", "this channel cannot be removed through the API", nil)
		return
	}
	if f.resolvedLocked(l)["enabled"] == true {
		writeError(w, http.StatusBadRequest, "slack_channel_enabled", "Claude is on in this channel", nil)
		return
	}
	f.removeLocked(l.id)
	sdktest.WriteJSON(w, http.StatusOK, map[string]any{"type": "slack_channel_deleted", "id": l.id})
}

// listChannels lists the set-up channels of one workspace, newest first;
// slack_workspace_id (Slack's ID) is required, a workspace the organization
// does not own is a 404 (#list-channels). With slack_channel_id it is a
// lookup: data holds the channel or nothing, no paging (#find-a-channel-by-its-slack-ids).
func (f *claudeTagFake) listChannels(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	ws := q.Get("slack_workspace_id")
	if !fakeSlackID.MatchString(ws) {
		writeError(w, http.StatusBadRequest, "", "slack_workspace_id is required", nil)
		return
	}
	if !f.checkLimit(w, r) {
		return
	}
	if !f.owned[ws] {
		writeError(w, http.StatusNotFound, "", "Slack workspace not found", nil)
		return
	}
	inherited := q.Get("include_inherited") == "true"
	if ch := q.Get("slack_channel_id"); q.Has("slack_channel_id") {
		if !fakeSlackChannelID.MatchString(ch) {
			writeError(w, http.StatusBadRequest, "", "invalid slack_channel_id", nil)
			return
		}
		data := []map[string]any{}
		if l := f.findSlackLocked("slack_channel", ws, ch); l != nil {
			data = append(data, f.levelJSONLocked(l, inherited))
		}
		sdktest.WriteJSON(w, http.StatusOK, map[string]any{"data": data, "next_page": nil})
		return
	}
	f.Paginate(w, r, sdktest.PageToken, f.pageSize, f.newestFirstLocked("slack_channel", ws, inherited))
}

// listWorkspaces lists the set-up workspaces, newest first. With
// slack_workspace_id: data holds it, or is empty when it is not set up, and a
// workspace the organization does not own is a 404 (#list-workspaces).
func (f *claudeTagFake) listWorkspaces(w http.ResponseWriter, r *http.Request) {
	if !f.checkLimit(w, r) {
		return
	}
	q := r.URL.Query()
	inherited := q.Get("include_inherited") == "true"
	if q.Has("slack_workspace_id") {
		ws := q.Get("slack_workspace_id")
		if !f.owned[ws] {
			writeError(w, http.StatusNotFound, "", "Slack workspace not found", nil)
			return
		}
		data := []map[string]any{}
		if l := f.findSlackLocked("slack_workspace", ws, ""); l != nil {
			data = append(data, f.levelJSONLocked(l, inherited))
		}
		sdktest.WriteJSON(w, http.StatusOK, map[string]any{"data": data, "next_page": nil})
		return
	}
	f.Paginate(w, r, sdktest.PageToken, f.pageSize, f.newestFirstLocked("slack_workspace", "", inherited))
}

// checkLimit: "limit defaults to 20, with a minimum of 1 and a maximum of
// 100; a value outside that range returns 400 on every list" (#pagination).
func (f *claudeTagFake) checkLimit(w http.ResponseWriter, r *http.Request) bool {
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err != nil || n < 1 || n > 100 {
			writeError(w, http.StatusBadRequest, "", "limit must be from 1 to 100", nil)
			return false
		}
	}
	return true
}

func (f *claudeTagFake) newestFirstLocked(kind, slackWorkspace string, inherited bool) []map[string]any {
	items := []map[string]any{}
	for _, l := range slices.Backward(f.levels) {
		if l.kind == kind && (slackWorkspace == "" || l.slackWorkspace == slackWorkspace) {
			items = append(items, f.levelJSONLocked(l, inherited))
		}
	}
	return items
}

// resolvedLocked is what applies in a level (#stored-and-inherited-values):
// each setting from the nearest level that sets it (channel, workspace,
// Slack defaults), instructions joined broadest first with a blank line,
// and the defaults enabled true and guest_policy restrict.
func (f *claudeTagFake) resolvedLocked(l *level) map[string]any {
	chain := []map[string]any{f.defaults}
	if l.kind == "slack_channel" {
		if ws := f.findSlackLocked("slack_workspace", l.slackWorkspace, ""); ws != nil {
			chain = append(chain, ws.settings)
		}
	}
	chain = append(chain, l.settings)
	out := map[string]any{"enabled": true, "instructions": nil, "model": nil, "environment_id": nil,
		"guest_policy": map[string]any{"type": "restrict"}}
	var texts []string
	for _, s := range chain {
		for _, key := range settingKeys {
			if v := s[key]; v != nil && key != "instructions" {
				out[key] = v
			}
		}
		if text, _ := s["instructions"].(string); text != "" {
			texts = append(texts, text)
		}
	}
	if len(texts) > 0 {
		out["instructions"] = strings.Join(texts, "\n\n")
	}
	return out
}

// levelJSONLocked renders a channel or workspace object (#slack-channels):
// settings stored, or what applies; sources always present.
func (f *claudeTagFake) levelJSONLocked(l *level, inherited bool) map[string]any {
	settings := map[string]any{}
	for _, key := range settingKeys {
		settings[key] = l.settings[key]
	}
	if inherited {
		settings = f.resolvedLocked(l)
	}
	self := map[string]any{"type": l.kind, l.kind + "_id": l.id}
	sources := map[string]any{"instructions": []any{}}
	for _, key := range settingKeys {
		if key != "instructions" && l.settings[key] != nil {
			sources[key] = self
		} else if key != "instructions" {
			sources[key] = nil
		}
	}
	if l.settings["instructions"] != nil {
		sources["instructions"] = []any{self}
	}
	slack := map[string]any{"workspace_id": l.slackWorkspace}
	if l.kind == "slack_channel" {
		slack["channel_id"] = l.slackChannel
	}
	return map[string]any{"type": l.kind, "id": l.id, "slack": slack, "settings": settings, "sources": sources,
		"created_at": l.createdAt, "updated_at": l.updatedAt}
}

func jsonEqual(a, b any) bool {
	ja, _ := json.Marshal(a)
	jb, _ := json.Marshal(b)
	return string(ja) == string(jb)
}

// --- Handlers: Bundles and Bundle targets ---

// listBundles lists the organization's Bundles, newest first (#list-bundles).
func (f *claudeTagFake) listBundles(w http.ResponseWriter, r *http.Request) {
	if f.checkLimit(w, r) {
		items := slices.Clone(f.bundles)
		slices.Reverse(items)
		f.Paginate(w, r, sdktest.PageToken, f.pageSize, items)
	}
}

// bundleOfPathLocked: "A bundle_id that is not a valid capp_ identifier
// returns 400. A valid one that does not exist or belongs to another
// organization returns 404, whatever else the request holds" (#bundles).
func (f *claudeTagFake) bundleOfPathLocked(w http.ResponseWriter, r *http.Request) (map[string]any, bool) {
	id := r.PathValue("bundle_id")
	if !strings.HasPrefix(id, "capp_") {
		writeError(w, http.StatusBadRequest, "", "invalid bundle_id", nil)
		return nil, false
	}
	for _, b := range f.bundles {
		if b["id"] == id {
			return b, true
		}
	}
	writeError(w, http.StatusNotFound, "", "Bundle not found", nil)
	return nil, false
}

func (f *claudeTagFake) getBundle(w http.ResponseWriter, r *http.Request) {
	if b, ok := f.bundleOfPathLocked(w, r); ok {
		sdktest.WriteJSON(w, http.StatusOK, b)
	}
}

func targetJSON(t *target) map[string]any {
	return map[string]any{"type": t.kind, "id": t.id, t.kind + "_id": t.ref, "created_at": t.createdAt}
}

// listTargets lists where a Bundle is attached, newest first (#list-a-bundles-targets).
func (f *claudeTagFake) listTargets(w http.ResponseWriter, r *http.Request) {
	b, ok := f.bundleOfPathLocked(w, r)
	if !ok || !f.checkLimit(w, r) {
		return
	}
	items := []map[string]any{}
	for _, t := range slices.Backward(f.targets) {
		if t.bundleID == b["id"] {
			items = append(items, targetJSON(t))
		}
	}
	f.Paginate(w, r, sdktest.PageToken, f.pageSize, items)
}

// attach: "The body names the type and the matching id. The channel or
// workspace must already be set up: attaching never sets a channel up.
// Attaching a pair that is already attached returns 200 with the existing
// Bundle target", and "the same pair always has the same id, also after a
// detach and a new attach" (#attach-a-bundle, #bundle-targets).
func (f *claudeTagFake) attach(w http.ResponseWriter, r *http.Request) {
	b, ok := f.bundleOfPathLocked(w, r)
	if !ok {
		return
	}
	var body map[string]string
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "", err.Error(), nil)
		return
	}
	kind := body["type"]
	ref, has := body[kind+"_id"]
	if (kind != "slack_channel" && kind != "slack_workspace") || !has || len(body) != 2 {
		writeError(w, http.StatusBadRequest, "", "the body names the type and the matching id", nil)
		return
	}
	if f.findLocked(kind, ref) == nil {
		writeError(w, http.StatusNotFound, "", kind+" not found", nil)
		return
	}
	bundleID := b["id"].(string)
	id := "btgt_" + strings.TrimPrefix(bundleID, "capp_") + "X" + ref[len("slch_"):]
	for _, t := range f.targets {
		if t.id == id {
			sdktest.WriteJSON(w, http.StatusOK, targetJSON(t))
			return
		}
	}
	t := &target{id: id, bundleID: bundleID, kind: kind, ref: ref, createdAt: f.nowLocked()}
	f.targets = append(f.targets, t)
	sdktest.WriteJSON(w, http.StatusOK, targetJSON(t))
}

// targetOfPathLocked: "A target_id that is not a valid btgt_ identifier
// returns 400. A valid one that belongs to another Bundle, was detached, or
// names a channel or workspace that is no longer set up returns 404"
// (#get-a-bundle-target).
func (f *claudeTagFake) targetOfPathLocked(w http.ResponseWriter, r *http.Request) (*target, bool) {
	b, ok := f.bundleOfPathLocked(w, r)
	if !ok {
		return nil, false
	}
	id := r.PathValue("target_id")
	if !strings.HasPrefix(id, "btgt_") {
		writeError(w, http.StatusBadRequest, "", "invalid target_id", nil)
		return nil, false
	}
	for _, t := range f.targets {
		if t.id == id && t.bundleID == b["id"] {
			return t, true
		}
	}
	writeError(w, http.StatusNotFound, "", "Bundle target not found", nil)
	return nil, false
}

func (f *claudeTagFake) getTarget(w http.ResponseWriter, r *http.Request) {
	if t, ok := f.targetOfPathLocked(w, r); ok {
		sdktest.WriteJSON(w, http.StatusOK, targetJSON(t))
	}
}

// detach: "A repeated detach returns 404" (#detach-a-bundle).
func (f *claudeTagFake) detach(w http.ResponseWriter, r *http.Request) {
	t, ok := f.targetOfPathLocked(w, r)
	if !ok {
		return
	}
	f.targets = slices.DeleteFunc(f.targets, func(x *target) bool { return x == t })
	sdktest.WriteJSON(w, http.StatusOK, map[string]any{"type": "bundle_target_deleted", "id": t.id})
}

// detachOutOfBand detaches a Bundle in claude.ai.
func (f *claudeTagFake) detachOutOfBand(targetID string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.targets = slices.DeleteFunc(f.targets, func(t *target) bool { return t.id == targetID })
}

// targetIDs returns the IDs of the Bundle targets, creation order.
func (f *claudeTagFake) targetIDs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var ids []string
	for _, t := range f.targets {
		ids = append(ids, t.id)
	}
	return ids
}
