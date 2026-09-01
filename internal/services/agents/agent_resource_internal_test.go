// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
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

// TestBuildAgentToolConfigParams is a regression test for the anthropic-sdk-go
// upgrade (v1.58 -> v1.67) that replaced the flat
// BetaManagedAgentsAgentToolConfigParams (a `name` enum plus shared `enabled` /
// `permission_policy` fields) with a discriminated union carrying one struct per
// built-in tool. The Terraform `name` attribute now selects the union branch, so
// this asserts every schema-accepted name lands on its own branch with the
// enabled flag and permission policy carried across.
func TestBuildAgentToolConfigParams(t *testing.T) {
	t.Parallel()

	// Each case checks the branch the name must select, and reads back the
	// enabled/permission-policy fields from that branch.
	tests := []struct {
		name    string
		extract func(anthropic.BetaManagedAgentsAgentToolConfigParamsUnion) (set bool, enabled param.Opt[bool], allow bool, ask bool)
	}{
		{"bash", func(u anthropic.BetaManagedAgentsAgentToolConfigParamsUnion) (bool, param.Opt[bool], bool, bool) {
			c := u.OfBash
			if c == nil {
				return false, param.Opt[bool]{}, false, false
			}
			return true, c.Enabled, c.PermissionPolicy.OfAlwaysAllow != nil, c.PermissionPolicy.OfAlwaysAsk != nil
		}},
		{"edit", func(u anthropic.BetaManagedAgentsAgentToolConfigParamsUnion) (bool, param.Opt[bool], bool, bool) {
			c := u.OfEdit
			if c == nil {
				return false, param.Opt[bool]{}, false, false
			}
			return true, c.Enabled, c.PermissionPolicy.OfAlwaysAllow != nil, c.PermissionPolicy.OfAlwaysAsk != nil
		}},
		{"read", func(u anthropic.BetaManagedAgentsAgentToolConfigParamsUnion) (bool, param.Opt[bool], bool, bool) {
			c := u.OfRead
			if c == nil {
				return false, param.Opt[bool]{}, false, false
			}
			return true, c.Enabled, c.PermissionPolicy.OfAlwaysAllow != nil, c.PermissionPolicy.OfAlwaysAsk != nil
		}},
		{"write", func(u anthropic.BetaManagedAgentsAgentToolConfigParamsUnion) (bool, param.Opt[bool], bool, bool) {
			c := u.OfWrite
			if c == nil {
				return false, param.Opt[bool]{}, false, false
			}
			return true, c.Enabled, c.PermissionPolicy.OfAlwaysAllow != nil, c.PermissionPolicy.OfAlwaysAsk != nil
		}},
		{"glob", func(u anthropic.BetaManagedAgentsAgentToolConfigParamsUnion) (bool, param.Opt[bool], bool, bool) {
			c := u.OfGlob
			if c == nil {
				return false, param.Opt[bool]{}, false, false
			}
			return true, c.Enabled, c.PermissionPolicy.OfAlwaysAllow != nil, c.PermissionPolicy.OfAlwaysAsk != nil
		}},
		{"grep", func(u anthropic.BetaManagedAgentsAgentToolConfigParamsUnion) (bool, param.Opt[bool], bool, bool) {
			c := u.OfGrep
			if c == nil {
				return false, param.Opt[bool]{}, false, false
			}
			return true, c.Enabled, c.PermissionPolicy.OfAlwaysAllow != nil, c.PermissionPolicy.OfAlwaysAsk != nil
		}},
		{"web_fetch", func(u anthropic.BetaManagedAgentsAgentToolConfigParamsUnion) (bool, param.Opt[bool], bool, bool) {
			c := u.OfWebFetch
			if c == nil {
				return false, param.Opt[bool]{}, false, false
			}
			return true, c.Enabled, c.PermissionPolicy.OfAlwaysAllow != nil, c.PermissionPolicy.OfAlwaysAsk != nil
		}},
		{"web_search", func(u anthropic.BetaManagedAgentsAgentToolConfigParamsUnion) (bool, param.Opt[bool], bool, bool) {
			c := u.OfWebSearch
			if c == nil {
				return false, param.Opt[bool]{}, false, false
			}
			return true, c.Enabled, c.PermissionPolicy.OfAlwaysAllow != nil, c.PermissionPolicy.OfAlwaysAsk != nil
		}},
	}

	for _, tc := range tests {
		t.Run(tc.name+"/always_allow", func(t *testing.T) {
			t.Parallel()

			u, diags := buildAgentToolConfigParams(tc.name, param.NewOpt(true), "always_allow")
			if diags.HasError() {
				t.Fatalf("unexpected diagnostics: %v", diags)
			}
			set, enabled, allow, ask := tc.extract(u)
			if !set {
				t.Fatalf("name %q did not select its own union branch", tc.name)
			}
			if !enabled.Valid() || !enabled.Value {
				t.Errorf("enabled = %+v, want set to true", enabled)
			}
			if !allow || ask {
				t.Errorf("permission policy = (allow=%t, ask=%t), want (true, false)", allow, ask)
			}
		})

		t.Run(tc.name+"/always_ask", func(t *testing.T) {
			t.Parallel()

			u, diags := buildAgentToolConfigParams(tc.name, param.NewOpt(false), "always_ask")
			if diags.HasError() {
				t.Fatalf("unexpected diagnostics: %v", diags)
			}
			set, enabled, allow, ask := tc.extract(u)
			if !set {
				t.Fatalf("name %q did not select its own union branch", tc.name)
			}
			if !enabled.Valid() || enabled.Value {
				t.Errorf("enabled = %+v, want set to false", enabled)
			}
			if allow || !ask {
				t.Errorf("permission policy = (allow=%t, ask=%t), want (false, true)", allow, ask)
			}
		})

		// An omitted permission_policy must leave both variants unset so the
		// field is elided from the request and the toolset default applies.
		t.Run(tc.name+"/no_policy", func(t *testing.T) {
			t.Parallel()

			u, diags := buildAgentToolConfigParams(tc.name, param.Opt[bool]{}, "")
			if diags.HasError() {
				t.Fatalf("unexpected diagnostics: %v", diags)
			}
			set, enabled, allow, ask := tc.extract(u)
			if !set {
				t.Fatalf("name %q did not select its own union branch", tc.name)
			}
			if enabled.Valid() {
				t.Errorf("enabled = %+v, want unset", enabled)
			}
			if allow || ask {
				t.Errorf("permission policy = (allow=%t, ask=%t), want both unset", allow, ask)
			}
		})
	}
}

// TestBuildAgentToolConfigParams_unknownName guards the union's default branch:
// a name outside the eight built-ins must surface a diagnostic rather than
// silently produce an empty union that marshals without a discriminator.
func TestBuildAgentToolConfigParams_unknownName(t *testing.T) {
	t.Parallel()

	_, diags := buildAgentToolConfigParams("not_a_tool", param.NewOpt(true), "always_allow")
	if !diags.HasError() {
		t.Fatal("expected an error diagnostic for an unsupported tool name, got none")
	}
	if got := diags.Errors()[0].Detail(); !strings.Contains(got, "not_a_tool") {
		t.Errorf("diagnostic detail = %q, want it to name the offending value", got)
	}
}
