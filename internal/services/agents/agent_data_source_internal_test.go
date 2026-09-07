// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

func TestAgentDataSourcesExposeModelEffortAndMultiagent(t *testing.T) {
	t.Parallel()

	t.Run("single agent", func(t *testing.T) {
		var resp datasource.SchemaResponse
		(&AgentDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &resp)

		if !resp.Schema.Attributes["model_effort"].IsComputed() {
			t.Fatal("model_effort must be computed")
		}
		if !resp.Schema.Attributes["multiagent"].IsComputed() {
			t.Fatal("multiagent must be computed")
		}
	})

	t.Run("agent list", func(t *testing.T) {
		var resp datasource.SchemaResponse
		(&AgentsDataSource{}).Schema(context.Background(), datasource.SchemaRequest{}, &resp)

		agents, ok := resp.Schema.Attributes["agents"].(schema.ListNestedAttribute)
		if !ok {
			t.Fatalf("agents attribute does not expose a nested object: %T", resp.Schema.Attributes["agents"])
		}
		attrs := agents.NestedObject.Attributes
		if !attrs["model_effort"].IsComputed() {
			t.Fatal("agents.model_effort must be computed")
		}
		if !attrs["multiagent"].IsComputed() {
			t.Fatal("agents.multiagent must be computed")
		}
	})
}

func TestAgentDataSourceMappersIncludeModelEffortAndResolvedMultiagent(t *testing.T) {
	t.Parallel()

	var agent anthropic.BetaManagedAgentsAgent
	err := json.Unmarshal([]byte(`{
		"id":"agent_coordinator",
		"archived_at":null,
		"created_at":"2026-08-05T12:00:00Z",
		"description":"",
		"mcp_servers":[],
		"metadata":{},
		"model":{"id":"claude-opus-4-6","effort":{"type":"high"}},
		"multiagent":{"type":"coordinator","agents":[{"type":"agent","id":"agent_worker","version":3}]},
		"name":"coordinator",
		"skills":[],
		"system":"",
		"tools":[],
		"type":"agent",
		"updated_at":"2026-08-05T12:00:00Z",
		"version":1
	}`), &agent)
	if err != nil {
		t.Fatalf("unmarshal agent: %v", err)
	}

	var single AgentDataSourceModel
	if diags := mapAgentResponseToDataSource(&agent, &single); diags.HasError() {
		t.Fatalf("map single-agent data source: %v", diags)
	}
	if got := single.ModelEffort.ValueString(); got != "high" {
		t.Fatalf("model_effort = %q, want high", got)
	}
	assertResolvedMultiagent(t, single.Multiagent)

	listValue, diags := mapAgentToDataSourceObject(&agent)
	if diags.HasError() {
		t.Fatalf("map agents data source: %v", diags)
	}
	listObject := listValue.(types.Object)
	if got := listObject.Attributes()["model_effort"].(types.String).ValueString(); got != "high" {
		t.Fatalf("agents.model_effort = %q, want high", got)
	}
	assertResolvedMultiagent(t, listObject.Attributes()["multiagent"].(types.Object))
}

func assertResolvedMultiagent(t *testing.T, value types.Object) {
	t.Helper()

	if value.IsNull() || value.IsUnknown() {
		t.Fatal("multiagent is null or unknown")
	}
	attrs := value.Attributes()
	if got := attrs["type"].(types.String).ValueString(); got != "coordinator" {
		t.Fatalf("multiagent.type = %q, want coordinator", got)
	}
	agents := attrs["agents"].(types.List).Elements()
	if len(agents) != 1 {
		t.Fatalf("multiagent.agents length = %d, want 1", len(agents))
	}
	ref := agents[0].(types.Object).Attributes()
	if got := ref["type"].(types.String).ValueString(); got != "agent" {
		t.Fatalf("multiagent.agents[0].type = %q, want agent", got)
	}
	if got := ref["id"].(types.String).ValueString(); got != "agent_worker" {
		t.Fatalf("multiagent.agents[0].id = %q, want agent_worker", got)
	}
	if got := ref["version"].(types.Int64).ValueInt64(); got != 3 {
		t.Fatalf("multiagent.agents[0].version = %d, want 3", got)
	}
}
