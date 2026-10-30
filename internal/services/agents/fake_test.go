// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents_test

import (
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"slices"
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
	// fakeDefaultEffort is the effort of every model: "Always present;
	// resolved to the per-model default at save time when not supplied"
	// (model.effort, agents/create).
	fakeDefaultEffort = "high"
	// fakeLatestSkillVersion is the latest version of every skill ("Version
	// to pin. Defaults to latest if omitted", skills[].version, agents/create).
	fakeLatestSkillVersion = "7"
)

// agentsFake is the agents API on an sdktest collection. An unknown ID
// answers 404 not_found_error (https://platform.claude.com/docs/en/api/errors).
// Stored agents are kept in the form every response carries (see resolve).
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

// newAgent is a stored agent before the body of its create: the lists come
// back empty, metadata as {} and multiagent as null (agents/create,
// "Returns"; listAfterEmptyResponse and tfvalue.MetadataAfterEmptyResponse
// map them), and execution_identity is "Always present; {"type":
// "service_account"} when runs carry no customer-named principal", which
// mapExecutionIdentityToState keeps null while the configuration leaves it
// unset.
func newAgent() map[string]any {
	return map[string]any{
		"type":               "agent",
		"metadata":           map[string]any{},
		"mcp_servers":        []any{},
		"skills":             []any{},
		"tools":              []any{},
		"multiagent":         nil,
		"execution_identity": map[string]any{"type": "service_account"},
		"version":            1,
		"archived_at":        nil,
	}
}

// create stores the agent the body describes at version 1.
func (f *agentsFake) create(w http.ResponseWriter, r *http.Request) {
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	obj := newAgent()
	for k, v := range body {
		if v != nil {
			obj[k] = v
		}
	}
	stored := f.resolve(f.agents.Create(obj))
	f.agents.Put(stored)
	sdktest.WriteJSON(w, http.StatusOK, stored)
}

// update applies an update (agents/update): every update bumps the version,
// which mapAgentResponseToState maps and buildUpdateParams sends back; an
// omitted field is kept and any other replaces the stored value as a whole;
// metadata is a patch, where a key set to null is deleted (the provider's
// tfvalue.MetadataPatch relies on it).
func (f *agentsFake) update(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("agent_id")
	stored, ok := f.agents.Get(id)
	if !ok {
		sdktest.WriteAPIError(w, http.StatusNotFound, "", "agent "+id+" not found")
		return
	}
	body, ok := f.DecodeBody(w, r)
	if !ok {
		return
	}
	version, _ := stored["version"].(json.Number).Int64()
	body["version"] = version + 1
	metadata, patchMetadata := body["metadata"]
	delete(body, "metadata")
	obj, _ := f.agents.Update(id, body, sdktest.Replace)
	if patchMetadata {
		obj, _ = f.agents.Update(id, map[string]any{"metadata": metadata}, sdktest.Patch)
	}
	obj = f.resolve(obj)
	f.agents.Put(obj)
	sdktest.WriteJSON(w, http.StatusOK, obj)
}

// list answers the agents in creation order, in pages of the page/next_page
// family, without the archived ones unless include_archived=true ("Include
// archived agents in results. Defaults to false"), and within the
// created_at[gte] / created_at[lte] bounds, both inclusive
// (https://platform.claude.com/docs/en/api/beta/agents/list).
func (f *agentsFake) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if f.pageFault != 0 && q.Get("page") != "" {
		sdktest.WriteAPIError(w, f.pageFault, "", "injected on a later page")
		return
	}
	gte, _ := time.Parse(time.RFC3339, q.Get("created_at[gte]"))
	lte, _ := time.Parse(time.RFC3339, q.Get("created_at[lte]"))
	items := f.agents.List(func(o map[string]any) bool {
		created, _ := time.Parse(time.RFC3339, o["created_at"].(string))
		return (q.Get("include_archived") == "true" || o["archived_at"] == nil) &&
			(gte.IsZero() || !created.Before(gte)) && (lte.IsZero() || !created.After(lte))
	})
	f.Paginate(w, r, sdktest.PageToken, f.listPageSize, items)
}

// resolve fills in what the API resolves and the provider maps back
// (agents/create, "Returns"):
//   - model.effort, sent as a level, answers as an object, "resolved to the
//     per-model default at save time when not supplied": the Computed
//     model_effort (modelEffortFromResponse), which modelEffortFollowsModel
//     leaves unknown when model changes;
//   - a skill without version is pinned to the latest one ("Defaults to
//     latest if omitted"), which skillsValue maps into skills[*].version;
//   - toolsets (resolveToolset) and multiagent references
//     (resolveMultiagent).
//
// Resolving a resolved agent changes nothing.
func (f *agentsFake) resolve(obj map[string]any) map[string]any {
	if m, ok := obj["model"].(map[string]any); ok {
		switch effort := m["effort"].(type) {
		case string:
			m["effort"] = map[string]any{"type": effort}
		case nil:
			m["effort"] = map[string]any{"type": fakeDefaultEffort}
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
			resolveToolset(t, "always_allow")
		case "mcp_toolset":
			// "MCP toolsets default to always_ask" (same page); enabled
			// "Defaults to true if not specified" (agents/create).
			resolveToolset(t, "always_ask")
		}
	}
	obj["multiagent"] = f.resolveMultiagent(obj["multiagent"], fmt.Sprint(obj["id"]), obj["version"])
	return obj
}

// resolveToolset fills a toolset's default_config ("Resolved default
// configuration"), which mapAgentToolsetToState and mapMCPToolsetsToState map
// into default_enabled and default_permission_policy, and the enabled and
// permission_policy of each per-tool config, which "Overrides the
// default_config setting" and otherwise inherits it: mapToolConfigsToState
// keeps an unset value null only while the API echoes the inherited default.
func resolveToolset(t map[string]any, defaultPolicy string) {
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
	for _, c := range objects(t["configs"]) {
		for _, key := range []string{"enabled", "permission_policy"} {
			if c[key] == nil {
				c[key] = def[key]
			}
		}
	}
}

// resolveMultiagent resolves the agent references of a multiagent
// configuration (agents/create, multiagent): a reference without version "is
// pinned to the agent's latest version", and "An agent can list itself ...
// with {"type": "self"}, which reads back as its own id and version"
// (https://platform.claude.com/docs/en/managed-agents/multiagent-orchestration).
// The coordinator roster echoes its advisor entry last, as the acceptance
// tests recorded (CLAUDE.md, multiagent). mapMultiagentToState depends on
// all three: it maps the pinned versions and correlates the roster by key,
// self as the owner's own ID, never by index.
func (f *agentsFake) resolveMultiagent(ma any, ownerID string, ownerVersion any) any {
	m, ok := ma.(map[string]any)
	if !ok {
		return ma
	}
	ref := func(a map[string]any) map[string]any {
		if a["type"] == "self" {
			return map[string]any{"type": "agent", "id": ownerID, "version": ownerVersion}
		}
		if stored, ok := f.agents.Get(fmt.Sprint(a["id"])); ok && a["version"] == nil {
			a["version"] = stored["version"]
		}
		return a
	}
	if m["type"] == "coordinator" {
		var agents, advisors []any
		for _, a := range objects(m["agents"]) {
			if a["type"] == "advisor" {
				advisors = append(advisors, a)
			} else {
				agents = append(agents, ref(a))
			}
		}
		m["agents"] = append(agents, advisors...)
	}
	for _, key := range []string{"subagents", "workflows"} {
		if member, ok := m[key].(map[string]any); ok && member["predefined_agents"] != nil {
			predefined := []any{}
			for _, a := range objects(member["predefined_agents"]) {
				predefined = append(predefined, ref(a))
			}
			member["predefined_agents"] = predefined
		}
	}
	return m
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
	obj := newAgent()
	obj["model"] = map[string]any{"id": "claude-sonnet-4-6"}
	maps.Copy(obj, body)
	f.agents.Put(f.resolve(f.agents.Create(obj)))
}

// assertRequestShape checks what every request of a test must carry: the
// managed-agents beta, the standard key and ?beta=true, on every route called.
func (f *agentsFake) assertRequestShape(t *testing.T) {
	t.Helper()
	f.AssertShape(sdktest.Shape{Credential: sdktest.APIKey, Beta: managedAgentsBeta, BetaQuery: true}, allAgentRoutes...)
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
