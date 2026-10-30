// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents_test

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// Routes of the agents endpoints (beta managed-agents-2026-04-01,
// ?beta=true, standard key). References:
// https://platform.claude.com/docs/en/api/beta/agents/create, .../list,
// .../retrieve, .../update and .../archive under the same path.
const (
	routeCreateAgent  = "POST /v1/agents"
	routeListAgents   = "GET /v1/agents"
	routeGetAgent     = "GET /v1/agents/{agent_id}"
	routeUpdateAgent  = "POST /v1/agents/{agent_id}"
	routeArchiveAgent = "POST /v1/agents/{agent_id}/archive"

	managedAgentsBeta = "managed-agents-2026-04-01"
)

var allAgentRoutes = []string{routeCreateAgent, routeListAgents, routeGetAgent, routeUpdateAgent, routeArchiveAgent}

// routeGetModel is the Models API "get" the plan-time lifecycle check calls
// (GA: no beta header, standard key). It takes a "Model identifier or alias"
// and returns lifecycle, deprecated_at and retires_at
// (https://platform.claude.com/docs/en/api/models/retrieve).
const routeGetModel = "GET /v1/models/{model_id}"

// activeModels are the models the agents tests name, served as active ("The
// model is available for use, open to new adopters, and not scheduled for
// retirement": deprecated_at and retires_at null).
var activeModels = []string{"claude-sonnet-4-6", "claude-opus-4-6", "claude-opus-5-5"}

// Values the fake resolves for fields a request leaves to the API.
const (
	// fakeDefaultEffort is the per-model default effort: "Always present;
	// resolved to the per-model default at save time when not supplied"
	// (model.effort, agents/create). The fake gives every model this one.
	fakeDefaultEffort = "high"
	// fakeLatestSkillVersion is the latest version of every skill: a skill
	// sent without version is pinned to it ("Version to pin. Defaults to
	// latest if omitted", skills[].version, agents/create).
	fakeLatestSkillVersion = "7"
	// maxListLimit is the largest page the list accepts ("Maximum results per
	// page. Default 20, maximum 100", agents/list).
	maxListLimit = 100
)

// agentsFake is the agents API on an sdktest collection. An unknown ID
// answers 404 not_found_error (https://platform.claude.com/docs/en/api/errors).
// Stored agents are kept in their resolved form, the one every response
// carries (see resolve).
type agentsFake struct {
	*sdktest.Server
	agents *sdktest.Collection
	// models is the Models API catalogue, by ID. An unknown model answers
	// 404 not_found_error.
	models *sdktest.Collection
	// listPageSize, when not 0, caps the pages of the list below the limit
	// the request sends, so a short list still spans several pages.
	listPageSize int
	// pageFault, when not 0, answers every list request that carries a page
	// cursor with this status, so a read fails after its first page.
	pageFault int
}

func newAgentsFake(t *testing.T) *agentsFake {
	t.Helper()
	srv := sdktest.NewServer(t)
	f := &agentsFake{Server: srv, agents: srv.NewCollection("agent_"), models: srv.NewCollection("")}
	f.Handle(routeCreateAgent, f.create)
	f.Handle(routeListAgents, f.list)
	f.agents.HandleGet(routeGetAgent)
	f.Handle(routeUpdateAgent, f.update)
	f.agents.HandleArchive(routeArchiveAgent)
	f.models.HandleGet(routeGetModel)
	for _, id := range activeModels {
		f.setModel(id, "active", nil, nil)
	}
	return f
}

// setModel serves model id with lifecycle and the two dates (RFC 3339
// strings, or nil for null), as ModelInfo carries them.
func (f *agentsFake) setModel(id, lifecycle string, deprecatedAt, retiresAt any) {
	f.models.Put(map[string]any{
		"type":          "model",
		"id":            id,
		"display_name":  id,
		"created_at":    "2025-01-01T00:00:00Z",
		"lifecycle":     lifecycle,
		"deprecated_at": deprecatedAt,
		"retires_at":    retiresAt,
	})
}

// modelLookups returns the number of lookups of model id.
func (f *agentsFake) modelLookups(id string) int {
	n := 0
	for _, r := range f.Requests(routeGetModel) {
		if r.Path == "/v1/models/"+id {
			n++
		}
	}
	return n
}

const agentAddr = "anthropic_agent.test"

// agentConfig is the provider block for the fake and one anthropic_agent with
// extra appended to its attributes.
func agentConfig(extra string) string {
	return providertest.ProviderConfig(sdktest.APIKey) + `
resource "anthropic_agent" "test" {
  name  = "test-agent"
  model = "claude-sonnet-4-6"
` + extra + `}
`
}

// create stores the agent the body describes, at version 1, in its resolved
// form. The lists the body omits come back empty, metadata as {} and
// multiagent as null (agents/create, "Returns").
func (f *agentsFake) create(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeBody(w, r)
	if !ok {
		return
	}
	if msg := f.invalid(body, nil); msg != "" {
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", msg)
		return
	}
	obj := map[string]any{
		"type":        "agent",
		"metadata":    map[string]any{},
		"mcp_servers": []any{},
		"skills":      []any{},
		"tools":       []any{},
		"multiagent":  nil,
		"version":     1,
		"archived_at": nil,
	}
	for k, v := range body {
		if v != nil {
			obj[k] = v
		}
	}
	stored := f.resolve(f.agents.Create(obj), nil)
	f.agents.Put(stored)
	sdktest.WriteJSON(w, http.StatusOK, stored)
}

// update follows https://platform.claude.com/docs/en/api/beta/agents/update:
// the version sent must be the stored one (409 otherwise) and every update
// bumps it; an omitted field is kept; metadata is a patch, where a key set to
// null is deleted and the other stored keys are kept; a multiagent_20261001
// configuration sent over a stored one of the same type is merged into it
// (mergeMultiagent); any other field replaces the stored value as a whole
// (model, tools, multiagent). The result is resolved like a create.
func (f *agentsFake) update(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("agent_id")
	stored, ok := f.agents.Get(id)
	if !ok {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "agent "+id+" not found")
		return
	}
	body, ok := decodeBody(w, r)
	if !ok {
		return
	}
	version, _ := stored["version"].(json.Number).Int64()
	if sent, present := body["version"]; present && fmt.Sprint(sent) != fmt.Sprint(version) {
		sdktest.WriteAPIError(w, http.StatusConflict, "", fmt.Sprintf("version %v is not the current version %d", sent, version))
		return
	}
	if msg := f.invalid(body, stored); msg != "" {
		sdktest.WriteAPIError(w, http.StatusBadRequest, "", msg)
		return
	}
	body["version"] = version + 1
	if ma, sent := body["multiagent"]; sent {
		body["multiagent"] = mergeMultiagent(ma, stored["multiagent"])
	}
	metadata, patchMetadata := body["metadata"]
	delete(body, "metadata")
	obj, _ := f.agents.Update(id, body, sdktest.Replace)
	if patchMetadata {
		obj, _ = f.agents.Update(id, map[string]any{"metadata": metadata}, sdktest.Patch)
	}
	obj = f.resolve(obj, stored)
	f.agents.Put(obj)
	sdktest.WriteJSON(w, http.StatusOK, obj)
}

// list answers the agents in creation order, in pages of the page/next_page
// family, without the archived ones unless include_archived=true ("Include
// archived agents in results. Defaults to false"), and within the
// created_at[gte] / created_at[lte] bounds, both inclusive
// (https://platform.claude.com/docs/en/api/beta/agents/list). A limit above
// 100 is a 400 invalid_request_error.
func (f *agentsFake) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if f.pageFault != 0 && q.Get("page") != "" {
		sdktest.WriteAPIError(w, f.pageFault, "", "injected on a later page")
		return
	}
	if v := q.Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err != nil || n > maxListLimit {
			sdktest.WriteAPIError(w, http.StatusBadRequest, "", fmt.Sprintf("limit: %s is above the maximum of %d", v, maxListLimit))
			return
		}
	}
	var bounds [2]time.Time
	for i, key := range []string{"created_at[gte]", "created_at[lte]"} {
		if v := q.Get(key); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				sdktest.WriteAPIError(w, http.StatusBadRequest, "", key+": not an RFC 3339 date-time")
				return
			}
			bounds[i] = t
		}
	}
	includeArchived := q.Get("include_archived") == "true"
	items := f.agents.List(func(o map[string]any) bool {
		created, _ := time.Parse(time.RFC3339, o["created_at"].(string))
		switch {
		case !includeArchived && o["archived_at"] != nil:
			return false
		case !bounds[0].IsZero() && created.Before(bounds[0]):
			return false
		case !bounds[1].IsZero() && created.After(bounds[1]):
			return false
		}
		return true
	})
	f.Paginate(w, r, sdktest.PageToken, f.listPageSize, items)
}

// invalid returns the message of the 400 the API answers for a body, or "".
// stored is the agent an update applies to (nil on create): a field the body
// omits keeps its stored value. Checked, from agents/create and
// agents/update:
//   - each mcp_toolset names a server of mcp_servers ("Must match a server
//     name from the mcp_servers array"), and each server is named by a
//     toolset ("Every server must be referenced by an mcp_toolset in the
//     agent's resulting tools; unreferenced servers are rejected");
//   - each agent a multiagent configuration references exists, is not
//     archived and has no multiagent itself ("Referenced agents must exist,
//     must not be archived, and must not themselves have multiagent set").
func (f *agentsFake) invalid(body, stored map[string]any) string {
	pick := func(key string) any {
		if v, sent := body[key]; sent || stored == nil {
			return v
		}
		return stored[key]
	}
	servers := map[string]bool{}
	for _, s := range objects(pick("mcp_servers")) {
		servers[fmt.Sprint(s["name"])] = false
	}
	for _, t := range objects(pick("tools")) {
		if t["type"] != "mcp_toolset" {
			continue
		}
		name := fmt.Sprint(t["mcp_server_name"])
		if _, ok := servers[name]; !ok {
			return fmt.Sprintf("tools: mcp_toolset references unknown MCP server %q", name)
		}
		servers[name] = true
	}
	for name, referenced := range servers {
		if !referenced {
			return fmt.Sprintf("mcp_servers: server %q is not referenced by any mcp_toolset", name)
		}
	}
	for _, id := range referencedAgents(body["multiagent"]) {
		a, ok := f.agents.Get(id)
		switch {
		case !ok:
			return fmt.Sprintf("multiagent: agent %q does not exist", id)
		case a["archived_at"] != nil:
			return fmt.Sprintf("multiagent: agent %q is archived", id)
		case a["multiagent"] != nil:
			return fmt.Sprintf("multiagent: agent %q has multiagent set (depth limit 1)", id)
		}
	}
	return ""
}

// referencedAgents returns the IDs of the agents a multiagent configuration
// references: the coordinator roster and the predefined_agents of
// multiagent_20261001, in their string or object form (self excluded).
func referencedAgents(ma any) []string {
	m, ok := ma.(map[string]any)
	if !ok {
		return nil
	}
	entries := m["agents"]
	var all []any
	if list, ok := entries.([]any); ok {
		all = append(all, list...)
	}
	for _, member := range []string{"subagents", "workflows"} {
		if mm, ok := m[member].(map[string]any); ok {
			if list, ok := mm["predefined_agents"].([]any); ok {
				all = append(all, list...)
			}
		}
	}
	var ids []string
	for _, e := range all {
		switch v := e.(type) {
		case string:
			ids = append(ids, v)
		case map[string]any:
			if v["type"] == "agent" {
				ids = append(ids, fmt.Sprint(v["id"]))
			}
		}
	}
	return ids
}

// resolve returns a stored agent in the form the API answers
// (agents/create, "Returns"), from the object an update or create produced
// and, on update, the agent before it (prev, nil on create):
//   - model is an object and effort is an object, resolved to the default
//     when a create omits it and kept when an update omits it ("on update,
//     omitting it leaves the stored value unchanged", agents/update);
//   - description and system sent as "" are cleared to null ("send empty
//     string or null to clear", agents/update);
//   - skills without version are pinned to the latest one;
//   - toolsets get their resolved default_config and per-tool configs
//     (resolveToolset);
//   - multiagent entries are resolved (resolveMultiagent);
//   - execution_identity is "Always present; {"type": "service_account"}
//     when runs carry no customer-named principal", and null restores that
//     default ("send null or {"type": "service_account"} to restore the
//     default", agents/update).
//
// Resolving a resolved agent changes nothing.
func (f *agentsFake) resolve(obj, prev map[string]any) map[string]any {
	obj["model"] = resolveModel(obj["model"], prev)
	for _, key := range []string{"description", "system"} {
		if obj[key] == "" {
			obj[key] = nil
		}
	}
	for _, s := range objects(obj["skills"]) {
		if s["version"] == nil {
			s["version"] = fakeLatestSkillVersion
		}
	}
	for _, t := range objects(obj["tools"]) {
		switch t["type"] {
		case "agent_toolset_20260401":
			// "If you omit it, the agent toolset is enabled with the default
			// permission policy, always_allow"
			// (https://platform.claude.com/docs/en/managed-agents/permission-policies).
			resolveToolset(t, "always_allow", true)
		case "mcp_toolset":
			// "MCP toolsets default to always_ask" (same page); enabled
			// "Defaults to true if not specified" (agents/create).
			resolveToolset(t, "always_ask", false)
		}
	}
	version := obj["version"]
	obj["multiagent"] = f.resolveMultiagent(obj["multiagent"], fmt.Sprint(obj["id"]), version)
	if obj["execution_identity"] == nil {
		obj["execution_identity"] = map[string]any{"type": "service_account"}
	}
	return obj
}

func resolveModel(model any, prev map[string]any) map[string]any {
	m, ok := model.(map[string]any)
	if !ok {
		m = map[string]any{"id": model}
	}
	switch effort := m["effort"].(type) {
	case string:
		m["effort"] = map[string]any{"type": effort}
	case nil:
		m["effort"] = map[string]any{"type": fakeDefaultEffort}
		if prev != nil {
			if pm, ok := prev["model"].(map[string]any); ok && pm["effort"] != nil {
				m["effort"] = pm["effort"]
			}
		}
	}
	return m
}

// resolveToolset fills a toolset's default_config ("Resolved default
// configuration") and the enabled and permission_policy of each per-tool
// config, which "Overrides the default_config setting" and otherwise
// inherits it. Configs are echoed, never added. A built-in tool config
// carries its name as its type, and a web_fetch url_sources is "always in
// the object form" (agents/create, "Returns").
func resolveToolset(t map[string]any, defaultPolicy string, builtin bool) {
	def, _ := t["default_config"].(map[string]any)
	if def == nil {
		def = map[string]any{}
	}
	if def["enabled"] == nil {
		def["enabled"] = true
	}
	if def["permission_policy"] == nil {
		def["permission_policy"] = map[string]any{"type": defaultPolicy}
	}
	t["default_config"] = def
	configs := objects(t["configs"])
	for _, c := range configs {
		if c["enabled"] == nil {
			c["enabled"] = def["enabled"]
		}
		if c["permission_policy"] == nil {
			c["permission_policy"] = def["permission_policy"]
		}
		if builtin {
			c["type"] = c["name"]
		}
		if src, ok := c["url_sources"].(map[string]any); ok {
			for key, v := range src {
				if s, ok := v.(string); ok {
					src[key] = map[string]any{"type": s}
				}
			}
		}
	}
	list := make([]any, len(configs))
	for i, c := range configs {
		list[i] = c
	}
	t["configs"] = list
}

// resolveMultiagent resolves the entries of a multiagent configuration
// (agents/create, multiagent): a reference without version "is pinned to
// the agent's latest version", and "An agent can list itself ... with
// {"type": "self"}, which reads back as its own id and version"
// (https://platform.claude.com/docs/en/managed-agents/multiagent-orchestration).
// The coordinator roster echoes its advisor entry last, as the acceptance
// tests recorded (CLAUDE.md, multiagent). A multiagent_20261001 member sent
// as null or omitted takes its default: subagents and workflows enabled with
// inline agents and no predefined agents, the advisor disabled ("Defaults
// to enabled", "Defaults to disabled", agents/create).
func (f *agentsFake) resolveMultiagent(ma any, ownerID string, ownerVersion any) any {
	m, ok := ma.(map[string]any)
	if !ok {
		return nil
	}
	ref := func(e any) map[string]any { return f.resolveReference(e, ownerID, ownerVersion) }
	switch m["type"] {
	case "coordinator":
		var agents, advisors []any
		for _, e := range m["agents"].([]any) {
			if a, ok := e.(map[string]any); ok && a["type"] == "advisor" {
				advisors = append(advisors, a)
				continue
			}
			agents = append(agents, ref(e))
		}
		m["agents"] = append(agents, advisors...)
	case "multiagent_20261001":
		for _, key := range []string{"subagents", "workflows"} {
			member, _ := m[key].(map[string]any)
			if member == nil {
				member = map[string]any{"type": "enabled"}
			}
			if member["type"] == "enabled" {
				if member["inline_agents"] == nil {
					member["inline_agents"] = map[string]any{"type": "enabled"}
				}
				predefined := []any{}
				if list, ok := member["predefined_agents"].([]any); ok {
					for _, e := range list {
						predefined = append(predefined, ref(e))
					}
				}
				member["predefined_agents"] = predefined
			}
			m[key] = member
		}
		if m["advisor"] == nil {
			m["advisor"] = map[string]any{"type": "disabled"}
		}
	}
	return m
}

// resolveReference resolves a roster or predefined_agents entry to an agent
// reference with a version.
func (f *agentsFake) resolveReference(e any, ownerID string, ownerVersion any) map[string]any {
	a, ok := e.(map[string]any)
	if !ok {
		a = map[string]any{"type": "agent", "id": e}
	}
	if a["type"] == "self" {
		return map[string]any{"type": "agent", "id": ownerID, "version": ownerVersion}
	}
	if a["version"] == nil {
		if ref, ok := f.agents.Get(fmt.Sprint(a["id"])); ok {
			a["version"] = ref["version"]
		}
	}
	return a
}

// mergeMultiagent applies a multiagent configuration an update sends to the
// stored one: "if the agent's stored multiagent also has type
// multiagent_20261001, this configuration is merged into the stored one,
// level by level, instead of replacing it. A key that the update omits keeps
// its stored value. A key sent as null takes its default ... An object sent
// with a type other than the stored one replaces the stored object ... A
// predefined_agents list that is sent replaces the stored list"
// (agents/update). Any other configuration replaces the stored one.
func mergeMultiagent(sent, stored any) any {
	s, ok := sent.(map[string]any)
	st, storedOK := stored.(map[string]any)
	if !ok || !storedOK || s["type"] != "multiagent_20261001" || st["type"] != "multiagent_20261001" {
		return sent
	}
	out := maps.Clone(st)
	for key, v := range s {
		sentMember, isObj := v.(map[string]any)
		storedMember, storedObj := st[key].(map[string]any)
		if isObj && storedObj && sentMember["type"] == storedMember["type"] {
			merged := maps.Clone(storedMember)
			maps.Copy(merged, sentMember)
			out[key] = merged
			continue
		}
		out[key] = v
	}
	return out
}

// objects returns the JSON objects of a JSON array value.
func objects(v any) []map[string]any {
	list, _ := v.([]any)
	var out []map[string]any
	for _, e := range list {
		if o, ok := e.(map[string]any); ok {
			out = append(out, o)
		}
	}
	return out
}

// seedAgent stores an agent created outside Terraform, resolved like a
// create.
func (f *agentsFake) seedAgent(body map[string]any) {
	obj := map[string]any{
		"type":        "agent",
		"model":       "claude-sonnet-4-6",
		"metadata":    map[string]any{},
		"mcp_servers": []any{},
		"skills":      []any{},
		"tools":       []any{},
		"multiagent":  nil,
		"version":     1,
		"archived_at": nil,
	}
	maps.Copy(obj, body)
	f.agents.Put(f.resolve(f.agents.Create(obj), nil))
}

// assertRequestShape checks what every request of a test must carry: the
// managed-agents beta, the standard key and ?beta=true, on every route called.
func (f *agentsFake) assertRequestShape(t *testing.T) {
	t.Helper()
	for _, route := range allAgentRoutes {
		if len(f.Requests(route)) == 0 {
			continue
		}
		f.AssertBeta(route, managedAgentsBeta)
		f.AssertCredential(route, sdktest.APIKey)
		for _, r := range f.Requests(route) {
			if r.Query.Get("beta") != "true" {
				t.Errorf("%s %s without ?beta=true", r.Method, r.Path)
			}
		}
	}
}

// checkAgentsArchived is a CheckDestroy: every agent left in the fake must
// be archived (destroying an anthropic_agent archives it; the API keeps
// archived agents).
func checkAgentsArchived(srv *agentsFake) resource.TestCheckFunc {
	return func(*terraform.State) error {
		for _, obj := range srv.agents.List(nil) {
			if at, _ := obj["archived_at"].(string); at == "" {
				return fmt.Errorf("%s was not archived", obj["id"])
			}
		}
		return nil
	}
}

// checkArchived is a CheckDestroy that requires the agents ids, and only
// them among those Terraform created, to be archived: agents seeded outside
// Terraform stay live.
func checkArchived(srv *agentsFake, ids ...string) resource.TestCheckFunc {
	return func(*terraform.State) error {
		for _, obj := range srv.agents.List(nil) {
			at, _ := obj["archived_at"].(string)
			if want := slices.Contains(ids, fmt.Sprint(obj["id"])); want != (at != "") {
				return fmt.Errorf("%s: archived_at = %q, want archived %v", obj["id"], at, want)
			}
		}
		return nil
	}
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
