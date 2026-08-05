// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

// TestMapAgentResponseToState_customToolInputSchema is a regression test for the
// anthropic-sdk-go upgrade (v1.37 -> v1.51) that changed how the
// BetaManagedAgentsCustomToolInputSchema struct marshals: field reordering plus a
// spurious "ExtraFields":null key. Re-marshaling the parsed schema with
// encoding/json produced a state value that no longer matched the user's planned
// input_schema string, triggering "Provider produced inconsistent result after
// apply". The mapping now preserves the raw API JSON via RawJSON() and stores it
// as a jsontypes.Normalized value, so semantic (key-order-insensitive) equality
// holds against the user's jsonencode() output.
func TestMapAgentResponseToState_customToolInputSchema(t *testing.T) {
	t.Parallel()

	// API response schema: "type" first, no ExtraFields key.
	const apiInputSchema = `{"type":"object","properties":{"email":{"description":"The user's email address","type":"string"}},"required":["email"]}`

	agentJSON := `{
		"id": "agent_123",
		"name": "test",
		"model": {"id": "claude-sonnet-4-6"},
		"version": 1,
		"created_at": "2026-01-01T00:00:00Z",
		"updated_at": "2026-01-01T00:00:00Z",
		"tools": [
			{
				"type": "custom",
				"name": "lookup_user",
				"description": "Look up a user",
				"input_schema": ` + apiInputSchema + `
			}
		]
	}`

	var agent anthropic.BetaManagedAgentsAgent
	if err := json.Unmarshal([]byte(agentJSON), &agent); err != nil {
		t.Fatalf("failed to unmarshal agent fixture: %s", err)
	}

	var data AgentResourceModel
	if diags := mapAgentResponseToState(context.Background(), &agent, &data); diags.HasError() {
		t.Fatalf("mapAgentResponseToState returned errors: %+v", diags)
	}

	var tools []agentCustomToolModel
	if diags := data.CustomTools.ElementsAs(context.Background(), &tools, false); diags.HasError() {
		t.Fatalf("failed to read custom tools: %+v", diags)
	}
	if len(tools) != 1 {
		t.Fatalf("expected 1 custom tool, got %d", len(tools))
	}

	got := tools[0].InputSchema
	if got.IsNull() || got.IsUnknown() {
		t.Fatalf("input_schema should be set, got null/unknown")
	}

	// The state value must not contain the encoding/json artifact that broke
	// round-tripping under the SDK upgrade.
	if strings.Contains(got.ValueString(), "ExtraFields") {
		t.Errorf("input_schema must not contain ExtraFields; got %q", got.ValueString())
	}

	// Semantic equality must hold against the user's jsonencode() output, which
	// orders keys alphabetically (properties, required, type) — a different order
	// than the API response. Without jsontypes.Normalized this mismatch is what
	// surfaced as "inconsistent result after apply".
	const userInputSchema = `{"properties":{"email":{"description":"The user's email address","type":"string"}},"required":["email"],"type":"object"}`
	planned := jsontypes.NewNormalizedValue(userInputSchema)
	equal, diags := planned.StringSemanticEquals(context.Background(), got)
	if diags.HasError() {
		t.Fatalf("semantic equality check returned errors: %+v", diags)
	}
	if !equal {
		t.Errorf("planned and applied input_schema should be semantically equal\nplanned: %s\napplied: %s", userInputSchema, got.ValueString())
	}
}

func TestMapAgentResponseToState_effortAndMultiagentSelf(t *testing.T) {
	t.Parallel()

	agentJSON := `{
		"id": "agent_coordinator",
		"name": "coordinator",
		"model": {"id": "claude-sonnet-4-6", "effort": {"type": "medium"}},
		"multiagent": {
			"type": "coordinator",
			"agents": [{"type": "agent", "id": "agent_coordinator", "version": 3}]
		},
		"version": 3,
		"created_at": "2026-01-01T00:00:00Z",
		"updated_at": "2026-01-01T00:00:00Z",
		"tools": []
	}`

	var agent anthropic.BetaManagedAgentsAgent
	if err := json.Unmarshal([]byte(agentJSON), &agent); err != nil {
		t.Fatalf("failed to unmarshal agent fixture: %s", err)
	}

	roster, diags := types.ListValue(
		types.ObjectType{AttrTypes: agentMultiagentEntryAttrTypes},
		[]attr.Value{types.ObjectValueMust(agentMultiagentEntryAttrTypes, map[string]attr.Value{
			"type":    types.StringValue("self"),
			"id":      types.StringNull(),
			"version": types.Int64Null(),
		})},
	)
	if diags.HasError() {
		t.Fatalf("failed to build roster state: %+v", diags)
	}

	data := AgentResourceModel{
		Multiagent: types.ObjectValueMust(agentMultiagentAttrTypes, map[string]attr.Value{
			"type":   types.StringValue("coordinator"),
			"agents": roster,
		}),
	}
	if diags := mapAgentResponseToState(context.Background(), &agent, &data); diags.HasError() {
		t.Fatalf("mapAgentResponseToState returned errors: %+v", diags)
	}

	if got := data.ModelEffort.ValueString(); got != "medium" {
		t.Fatalf("expected model_effort medium, got %q", got)
	}

	var topology agentMultiagentModel
	if diags := data.Multiagent.As(context.Background(), &topology, basetypes.ObjectAsOptions{}); diags.HasError() {
		t.Fatalf("failed to read multiagent state: %+v", diags)
	}
	var entries []agentMultiagentEntryModel
	if diags := topology.Agents.ElementsAs(context.Background(), &entries, false); diags.HasError() {
		t.Fatalf("failed to read roster state: %+v", diags)
	}
	if len(entries) != 1 || entries[0].Type.ValueString() != "self" {
		t.Fatalf("expected the configured self sentinel to be preserved, got %+v", entries)
	}
	if !entries[0].ID.IsNull() || !entries[0].Version.IsNull() {
		t.Fatalf("self entry must not gain a resolved id or version: %+v", entries[0])
	}
}
