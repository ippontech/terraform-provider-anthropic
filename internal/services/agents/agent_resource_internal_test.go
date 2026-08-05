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
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
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

func TestMapAgentResponseToState_importInfersResolvedSelf(t *testing.T) {
	t.Parallel()

	agentJSON := `{
		"id": "agent_coordinator",
		"name": "coordinator",
		"model": {"id": "claude-sonnet-4-6"},
		"multiagent": {
			"type": "coordinator",
			"agents": [
				{"type": "agent", "id": "agent_coordinator", "version": 3},
				{"type": "agent", "id": "agent_worker", "version": 2}
			]
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

	// Import initially populates only the resource ID, so no prior roster is
	// available to identify the API's resolved self-reference.
	data := AgentResourceModel{Multiagent: types.ObjectNull(agentMultiagentAttrTypes)}
	if diags := mapAgentResponseToState(context.Background(), &agent, &data); diags.HasError() {
		t.Fatalf("mapAgentResponseToState returned errors: %+v", diags)
	}

	var topology agentMultiagentModel
	if diags := data.Multiagent.As(context.Background(), &topology, basetypes.ObjectAsOptions{}); diags.HasError() {
		t.Fatalf("failed to read multiagent state: %+v", diags)
	}
	var entries []agentMultiagentEntryModel
	if diags := topology.Agents.ElementsAs(context.Background(), &entries, false); diags.HasError() {
		t.Fatalf("failed to read roster state: %+v", diags)
	}
	if len(entries) != 2 {
		t.Fatalf("expected two roster entries, got %d", len(entries))
	}
	if entries[0].Type.ValueString() != "self" || !entries[0].ID.IsNull() || !entries[0].Version.IsNull() {
		t.Fatalf("expected resolved owner reference to map to self, got %+v", entries[0])
	}
	if entries[1].Type.ValueString() != "agent" || entries[1].ID.ValueString() != "agent_worker" || entries[1].Version.ValueInt64() != 2 {
		t.Fatalf("expected worker reference to remain concrete, got %+v", entries[1])
	}
}

func TestMultiagentRosterEntryValidator(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		entryType   types.String
		id          types.String
		version     types.Int64
		wantSummary string
	}{
		{
			name:        "agent requires id",
			entryType:   types.StringValue("agent"),
			id:          types.StringNull(),
			version:     types.Int64Null(),
			wantSummary: "Missing agent ID",
		},
		{
			name:        "agent id must be nonempty",
			entryType:   types.StringValue("agent"),
			id:          types.StringValue("  "),
			version:     types.Int64Null(),
			wantSummary: "Invalid agent ID",
		},
		{
			name:        "self forbids id",
			entryType:   types.StringValue("self"),
			id:          types.StringValue("agent_123"),
			version:     types.Int64Null(),
			wantSummary: "Invalid self roster entry",
		},
		{
			name:        "self forbids version",
			entryType:   types.StringValue("self"),
			id:          types.StringNull(),
			version:     types.Int64Value(1),
			wantSummary: "Invalid self roster entry",
		},
		{
			name:      "valid agent",
			entryType: types.StringValue("agent"),
			id:        types.StringValue("agent_123"),
			version:   types.Int64Value(2),
		},
		{
			name:      "valid self",
			entryType: types.StringValue("self"),
			id:        types.StringNull(),
			version:   types.Int64Null(),
		},
		{
			name:      "unknown agent id is deferred",
			entryType: types.StringValue("agent"),
			id:        types.StringUnknown(),
			version:   types.Int64Unknown(),
		},
		{
			name:      "unknown type is deferred",
			entryType: types.StringUnknown(),
			id:        types.StringValue("agent_123"),
			version:   types.Int64Value(1),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			entry := types.ObjectValueMust(agentMultiagentEntryAttrTypes, map[string]attr.Value{
				"type":    tt.entryType,
				"id":      tt.id,
				"version": tt.version,
			})
			var resp validator.ObjectResponse
			multiagentRosterEntryValidator{}.ValidateObject(
				context.Background(),
				validator.ObjectRequest{Path: path.Root("entry"), ConfigValue: entry},
				&resp,
			)

			if tt.wantSummary == "" {
				if resp.Diagnostics.HasError() {
					t.Fatalf("unexpected diagnostics: %+v", resp.Diagnostics)
				}
				return
			}
			for _, diagnostic := range resp.Diagnostics {
				if diagnostic.Summary() == tt.wantSummary {
					return
				}
			}
			t.Fatalf("expected diagnostic %q, got %+v", tt.wantSummary, resp.Diagnostics)
		})
	}
}

func TestMultiagentRosterValidator(t *testing.T) {
	t.Parallel()

	entry := func(entryType string, id types.String) attr.Value {
		return types.ObjectValueMust(agentMultiagentEntryAttrTypes, map[string]attr.Value{
			"type":    types.StringValue(entryType),
			"id":      id,
			"version": types.Int64Null(),
		})
	}
	tests := []struct {
		name        string
		entries     []attr.Value
		wantSummary string
	}{
		{
			name: "valid distinct roster",
			entries: []attr.Value{
				entry("self", types.StringNull()),
				entry("agent", types.StringValue("agent_1")),
				entry("agent", types.StringValue("agent_2")),
			},
		},
		{
			name: "duplicate self",
			entries: []attr.Value{
				entry("self", types.StringNull()),
				entry("self", types.StringNull()),
			},
			wantSummary: "Duplicate self roster entry",
		},
		{
			name: "duplicate agent",
			entries: []attr.Value{
				entry("agent", types.StringValue("agent_1")),
				entry("agent", types.StringValue("agent_1")),
			},
			wantSummary: "Duplicate agent roster entry",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			roster := types.ListValueMust(types.ObjectType{AttrTypes: agentMultiagentEntryAttrTypes}, tt.entries)
			var resp validator.ListResponse
			multiagentRosterValidator{}.ValidateList(
				context.Background(),
				validator.ListRequest{Path: path.Root("agents"), ConfigValue: roster},
				&resp,
			)

			if tt.wantSummary == "" {
				if resp.Diagnostics.HasError() {
					t.Fatalf("unexpected diagnostics: %+v", resp.Diagnostics)
				}
				return
			}
			for _, diagnostic := range resp.Diagnostics {
				if diagnostic.Summary() == tt.wantSummary {
					return
				}
			}
			t.Fatalf("expected diagnostic %q, got %+v", tt.wantSummary, resp.Diagnostics)
		})
	}
}

func TestValidateResolvedMultiagentEntries(t *testing.T) {
	t.Parallel()

	entry := func(entryType types.String, id types.String, version types.Int64) agentMultiagentEntryModel {
		return agentMultiagentEntryModel{Type: entryType, ID: id, Version: version}
	}
	tests := []struct {
		name        string
		entries     []agentMultiagentEntryModel
		wantSummary string
	}{
		{
			name: "valid resolved roster",
			entries: []agentMultiagentEntryModel{
				entry(types.StringValue("self"), types.StringNull(), types.Int64Unknown()),
				entry(types.StringValue("agent"), types.StringValue("agent_1"), types.Int64Unknown()),
			},
		},
		{
			name: "resolved self id is rejected",
			entries: []agentMultiagentEntryModel{
				entry(types.StringValue("self"), types.StringValue("agent_1"), types.Int64Unknown()),
			},
			wantSummary: "Invalid self roster entry",
		},
		{
			name: "resolved self version is rejected",
			entries: []agentMultiagentEntryModel{
				entry(types.StringValue("self"), types.StringNull(), types.Int64Value(1)),
			},
			wantSummary: "Invalid self roster entry",
		},
		{
			name: "unresolved agent id is rejected before request",
			entries: []agentMultiagentEntryModel{
				entry(types.StringValue("agent"), types.StringUnknown(), types.Int64Unknown()),
			},
			wantSummary: "Invalid agent ID",
		},
		{
			name: "resolved duplicate agents are rejected",
			entries: []agentMultiagentEntryModel{
				entry(types.StringValue("agent"), types.StringValue("agent_1"), types.Int64Value(1)),
				entry(types.StringValue("agent"), types.StringValue("agent_1"), types.Int64Value(2)),
			},
			wantSummary: "Duplicate agent roster entry",
		},
		{
			name: "resolved invalid version is rejected",
			entries: []agentMultiagentEntryModel{
				entry(types.StringValue("agent"), types.StringValue("agent_1"), types.Int64Value(0)),
			},
			wantSummary: "Invalid agent version",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			diagnostics := validateResolvedMultiagentEntries(tt.entries)
			if tt.wantSummary == "" {
				if diagnostics.HasError() {
					t.Fatalf("unexpected diagnostics: %+v", diagnostics)
				}
				return
			}
			for _, diagnostic := range diagnostics {
				if diagnostic.Summary() == tt.wantSummary {
					return
				}
			}
			t.Fatalf("expected diagnostic %q, got %+v", tt.wantSummary, diagnostics)
		})
	}
}
