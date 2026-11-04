// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

// Routes of the agents endpoints anthropic_agent uses (beta
// managed-agents-2026-04-01, ?beta=true, standard key). References:
// https://platform.claude.com/docs/en/api/beta/agents/create, .../retrieve,
// .../update and .../archive under the same path.
const (
	routeCreateAgent  = "POST /v1/agents"
	routeGetAgent     = "GET /v1/agents/{agent_id}"
	routeUpdateAgent  = "POST /v1/agents/{agent_id}"
	routeArchiveAgent = "POST /v1/agents/{agent_id}/archive"

	managedAgentsBeta = "managed-agents-2026-04-01"
)

var allAgentRoutes = []string{routeCreateAgent, routeGetAgent, routeUpdateAgent, routeArchiveAgent}

// agentsFake is the agents API on an sdktest collection. An unknown ID
// answers 404 not_found_error (https://platform.claude.com/docs/en/api/errors).
type agentsFake struct {
	*sdktest.Server
	agents *sdktest.Collection
}

func newAgentsFake(t *testing.T) *agentsFake {
	t.Helper()
	srv := sdktest.NewServer(t)
	f := &agentsFake{Server: srv, agents: srv.NewCollection("agent_")}
	f.Handle(routeCreateAgent, f.create)
	f.agents.HandleGet(routeGetAgent)
	f.Handle(routeUpdateAgent, f.update)
	f.agents.HandleArchive(routeArchiveAgent)
	return f
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

// create stores the agent the body describes, at version 1. The lists the
// body omits come back empty, metadata as {} and multiagent as null.
func (f *agentsFake) create(w http.ResponseWriter, r *http.Request) {
	body, ok := decodeBody(w, r)
	if !ok {
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
	sdktest.WriteJSON(w, http.StatusOK, f.agents.Create(obj))
}

// update follows https://platform.claude.com/docs/en/api/beta/agents/update:
// the version sent must be the stored one (409 otherwise) and every update
// bumps it; an omitted field is kept; metadata is a patch, where a key set to
// null is deleted and the other stored keys are kept; any other field
// replaces the stored value as a whole (model, multiagent).
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
	body["version"] = version + 1
	metadata, patchMetadata := body["metadata"]
	delete(body, "metadata")
	obj, _ := f.agents.Update(id, body, sdktest.Replace)
	if patchMetadata {
		obj, _ = f.agents.Update(id, map[string]any{"metadata": metadata}, sdktest.Patch)
	}
	sdktest.WriteJSON(w, http.StatusOK, obj)
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
