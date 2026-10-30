// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

func multiagentEntryObj(t *testing.T, typ string, id types.String, version types.Int64, model types.String) attr.Value {
	t.Helper()
	v, d := multiagentEntryValue(typ, id, version, model)
	if d.HasError() {
		t.Fatalf("entry: %+v", d)
	}
	return v
}

func multiagentObj(t *testing.T, entries ...attr.Value) types.Object {
	t.Helper()
	obj, d := multiagentObject("coordinator", entries, nil)
	if d.HasError() {
		t.Fatalf("object: %+v", d)
	}
	return obj
}

func decodeEntries(t *testing.T, obj types.Object) []agentMultiagentEntryModel {
	t.Helper()
	var ma agentMultiagentModel
	if d := obj.As(context.Background(), &ma, basetypes.ObjectAsOptions{}); d.HasError() {
		t.Fatalf("as: %+v", d)
	}
	var out []agentMultiagentEntryModel
	if d := ma.Agents.ElementsAs(context.Background(), &out, false); d.HasError() {
		t.Fatalf("elements: %+v", d)
	}
	return out
}

func TestBuildMultiagentParams_allEntryKinds(t *testing.T) {
	t.Parallel()
	obj := multiagentObj(t,
		multiagentEntryObj(t, "agent", types.StringValue("agent_a"), types.Int64Value(3), types.StringNull()),
		multiagentEntryObj(t, "agent", types.StringValue("agent_b"), types.Int64Unknown(), types.StringNull()),
		multiagentEntryObj(t, "self", types.StringNull(), types.Int64Unknown(), types.StringNull()),
		multiagentEntryObj(t, "advisor", types.StringNull(), types.Int64Null(), types.StringValue("claude-opus-4-6")),
	)
	p, diags := buildMultiagentParams(context.Background(), obj, types.ObjectNull(agentMultiagentAttrTypes))
	if diags.HasError() {
		t.Fatalf("diags: %+v", diags)
	}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"agents":[{"id":"agent_a","type":"agent","version":3},{"id":"agent_b","type":"agent"},{"type":"self"},{"model":"claude-opus-4-6","type":"advisor"}],"type":"coordinator"}`
	if string(b) != want {
		t.Fatalf("got  %s\nwant %s", b, want)
	}
}

func TestBuildMultiagentParams_nullIsOmitted(t *testing.T) {
	t.Parallel()
	p, diags := buildMultiagentParams(context.Background(), types.ObjectNull(agentMultiagentAttrTypes), types.ObjectNull(agentMultiagentAttrTypes))
	if diags.HasError() {
		t.Fatalf("diags: %+v", diags)
	}
	b, _ := json.Marshal(anthropic.BetaAgentNewParams{Multiagent: p})
	if strings.Contains(string(b), "multiagent") {
		t.Fatalf("null roster must be omitted on create: %s", b)
	}
}

func TestUpdateParams_clearSendsNull(t *testing.T) {
	t.Parallel()
	params := anthropic.BetaAgentUpdateParams{}
	params.SetExtraFields(multiagentClearField())
	b, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"multiagent":null`) {
		t.Fatalf("expected explicit multiagent null, got %s", b)
	}
	b, _ = json.Marshal(anthropic.BetaAgentUpdateParams{})
	if strings.Contains(string(b), "multiagent") {
		t.Fatalf("zero params must omit multiagent: %s", b)
	}
}

func apiAgentWithMultiagent(t *testing.T, raw string) *anthropic.BetaManagedAgentsAgent {
	t.Helper()
	j := `{"id":"agent_owner","name":"n","model":{"id":"claude-sonnet-4-6"},"version":2,
		"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"` + raw + `}`
	var a anthropic.BetaManagedAgentsAgent
	if err := json.Unmarshal([]byte(j), &a); err != nil {
		t.Fatal(err)
	}
	return &a
}

func TestMapAgentResponseToState_multiagentAbsent(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{``, `,"multiagent":null`} {
		agent := apiAgentWithMultiagent(t, raw)
		data := AgentResourceModel{Multiagent: multiagentObj(t,
			multiagentEntryObj(t, "self", types.StringNull(), types.Int64Null(), types.StringNull()))}
		if d := mapAgentResponseToState(context.Background(), agent, &data, readMapping); d.HasError() {
			t.Fatalf("diags: %+v", d)
		}
		if !data.Multiagent.IsNull() {
			t.Fatalf("raw %q: expected null multiagent, got %v", raw, data.Multiagent)
		}
	}
}

const multiagentAPIRoster = `,"multiagent":{"type":"coordinator","agents":[
	{"type":"agent","id":"agent_b","version":4},
	{"type":"agent","id":"agent_owner","version":2},
	{"type":"agent","id":"agent_a","version":7},
	{"type":"advisor","model":"claude-opus-4-6"}]}`

func TestMapMultiagentToState_correlatesByKeyNotIndex(t *testing.T) {
	t.Parallel()
	agent := apiAgentWithMultiagent(t, multiagentAPIRoster)
	// Config order differs from the API's: advisor first, self in the middle.
	prior := multiagentObj(t,
		multiagentEntryObj(t, "advisor", types.StringNull(), types.Int64Null(), types.StringValue("claude-opus-4-6")),
		multiagentEntryObj(t, "agent", types.StringValue("agent_a"), types.Int64Unknown(), types.StringNull()),
		multiagentEntryObj(t, "self", types.StringNull(), types.Int64Unknown(), types.StringNull()),
		multiagentEntryObj(t, "agent", types.StringValue("agent_b"), types.Int64Value(4), types.StringNull()),
	)
	got, diags := mapMultiagentToState(context.Background(), agent.Multiagent, agent.ID, prior)
	if diags.HasError() {
		t.Fatalf("diags: %+v", diags)
	}
	e := decodeEntries(t, got)
	if len(e) != 4 {
		t.Fatalf("expected 4 entries, got %d: %+v", len(e), e)
	}
	check := func(i int, typ, id string, version int64, model string) {
		t.Helper()
		if e[i].Type.ValueString() != typ || e[i].ID.ValueString() != id ||
			e[i].Version.ValueInt64() != version || e[i].Model.ValueString() != model {
			t.Errorf("entry %d = %+v, want %s/%s/%d/%s", i, e[i], typ, id, version, model)
		}
	}
	check(0, "advisor", "", 0, "claude-opus-4-6")
	check(1, "agent", "agent_a", 7, "")
	check(2, "self", "", 0, "")
	if !e[2].ID.IsNull() || !e[2].Version.IsNull() {
		t.Errorf("self entry must keep null id/version: %+v", e[2])
	}
	check(3, "agent", "agent_b", 4, "")
}

func TestMapMultiagentToState_explicitOwnerReferenceIsNotSelf(t *testing.T) {
	t.Parallel()
	agent := apiAgentWithMultiagent(t, `,"multiagent":{"type":"coordinator","agents":[{"type":"agent","id":"agent_owner","version":2}]}`)
	prior := multiagentObj(t, multiagentEntryObj(t, "agent", types.StringValue("agent_owner"), types.Int64Unknown(), types.StringNull()))
	got, _ := mapMultiagentToState(context.Background(), agent.Multiagent, agent.ID, prior)
	e := decodeEntries(t, got)
	if len(e) != 1 || e[0].Type.ValueString() != "agent" || e[0].ID.ValueString() != "agent_owner" || e[0].Version.ValueInt64() != 2 {
		t.Fatalf("explicit owner reference must stay an agent entry: %+v", e)
	}
}

// A self entry is correlated after the explicit references, whatever the
// order: listed first, it still does not take the entry an explicit
// reference to the owner's own ID names.
func TestMapMultiagentToState_selfAfterExplicitReferences(t *testing.T) {
	t.Parallel()
	agent := apiAgentWithMultiagent(t, `,"multiagent":{"type":"coordinator","agents":[{"type":"agent","id":"agent_owner","version":2}]}`)
	prior := multiagentObj(t,
		multiagentEntryObj(t, "self", types.StringNull(), types.Int64Null(), types.StringNull()),
		multiagentEntryObj(t, "agent", types.StringValue("agent_owner"), types.Int64Unknown(), types.StringNull()))
	got, diags := mapMultiagentToState(context.Background(), agent.Multiagent, agent.ID, prior)
	if diags.HasError() {
		t.Fatalf("diags: %+v", diags)
	}
	want := multiagentObj(t, multiagentEntryObj(t, "agent", types.StringValue("agent_owner"), types.Int64Value(2), types.StringNull()))
	requireMultiagentEqual(t, got, want)
}

func TestMapMultiagentToState_driftAndRemoval(t *testing.T) {
	t.Parallel()
	agent := apiAgentWithMultiagent(t, multiagentAPIRoster)
	// Config knows agent_a and an agent the API no longer has: the latter is dropped,
	// the three API entries config does not know are appended as drift.
	prior := multiagentObj(t,
		multiagentEntryObj(t, "agent", types.StringValue("agent_gone"), types.Int64Null(), types.StringNull()),
		multiagentEntryObj(t, "agent", types.StringValue("agent_a"), types.Int64Null(), types.StringNull()),
	)
	got, _ := mapMultiagentToState(context.Background(), agent.Multiagent, agent.ID, prior)
	e := decodeEntries(t, got)
	if len(e) != 4 {
		t.Fatalf("expected agent_a + 3 drift entries, got %+v", e)
	}
	if e[0].ID.ValueString() != "agent_a" {
		t.Errorf("configured entry must come first: %+v", e[0])
	}
	if e[3].Type.ValueString() != "advisor" {
		t.Errorf("drift entries keep API order and API type: %+v", e)
	}
}

func TestMapMultiagentToState_importMapsVerbatim(t *testing.T) {
	t.Parallel()
	agent := apiAgentWithMultiagent(t, multiagentAPIRoster)
	got, diags := mapMultiagentToState(context.Background(), agent.Multiagent, agent.ID, types.ObjectNull(agentMultiagentAttrTypes))
	if diags.HasError() {
		t.Fatalf("diags: %+v", diags)
	}
	e := decodeEntries(t, got)
	if len(e) != 4 || e[0].ID.ValueString() != "agent_b" || e[1].ID.ValueString() != "agent_owner" || e[3].Type.ValueString() != "advisor" {
		t.Fatalf("verbatim mapping expected: %+v", e)
	}
}

func TestValidateMultiagentConfig(t *testing.T) {
	t.Parallel()
	str, null, unk := types.StringValue, types.StringNull(), types.StringUnknown()
	nv := types.Int64Null()
	tests := []struct {
		name    string
		entries []attr.Value
		wantErr bool
	}{
		{"valid mix", []attr.Value{
			multiagentEntryObj(t, "agent", str("a"), nv, null),
			multiagentEntryObj(t, "self", null, nv, null),
			multiagentEntryObj(t, "advisor", null, nv, str("m")),
		}, false},
		{"agent without id", []attr.Value{multiagentEntryObj(t, "agent", null, nv, null)}, true},
		{"agent with unknown id", []attr.Value{multiagentEntryObj(t, "agent", unk, nv, null)}, false},
		{"agent with model", []attr.Value{multiagentEntryObj(t, "agent", str("a"), nv, str("m"))}, true},
		{"advisor without model", []attr.Value{multiagentEntryObj(t, "advisor", null, nv, null)}, true},
		{"advisor with unknown model", []attr.Value{multiagentEntryObj(t, "advisor", null, nv, unk)}, false},
		{"advisor with id", []attr.Value{multiagentEntryObj(t, "advisor", str("a"), nv, str("m"))}, true},
		{"self with id", []attr.Value{multiagentEntryObj(t, "self", str("a"), nv, null)}, true},
		{"self with version", []attr.Value{multiagentEntryObj(t, "self", null, types.Int64Value(1), null)}, true},
		{"advisor with version", []attr.Value{multiagentEntryObj(t, "advisor", null, types.Int64Value(1), str("m"))}, true},
		{"duplicate agent ids", []attr.Value{
			multiagentEntryObj(t, "agent", str("a"), nv, null), multiagentEntryObj(t, "agent", str("a"), nv, null),
		}, true},
		{"distinct agent ids", []attr.Value{
			multiagentEntryObj(t, "agent", str("a"), nv, null), multiagentEntryObj(t, "agent", str("b"), nv, null),
		}, false},
		{"unknown agent ids are not duplicates", []attr.Value{
			multiagentEntryObj(t, "agent", unk, nv, null), multiagentEntryObj(t, "agent", unk, nv, null),
		}, false},
		{"two selfs", []attr.Value{
			multiagentEntryObj(t, "self", null, nv, null), multiagentEntryObj(t, "self", null, nv, null),
		}, true},
		{"two advisors", []attr.Value{
			multiagentEntryObj(t, "advisor", null, nv, str("m")), multiagentEntryObj(t, "advisor", null, nv, str("m")),
		}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := validateMultiagentConfig(context.Background(), multiagentObj(t, tc.entries...))
			if d.HasError() != tc.wantErr {
				t.Fatalf("wantErr=%v, diags=%+v", tc.wantErr, d)
			}
		})
	}
	if d := validateMultiagentConfig(context.Background(), types.ObjectUnknown(agentMultiagentAttrTypes)); d.HasError() {
		t.Fatalf("unknown object must be accepted: %+v", d)
	}
}

func TestMapAgentResponseToDataSource_multiagent(t *testing.T) {
	t.Parallel()
	agent := apiAgentWithMultiagent(t, `,"multiagent":{"type":"coordinator","agents":[
		{"type":"agent","id":"agent_owner","version":1},
		{"type":"advisor","model":"claude-opus-4-6"}]}`)

	var data AgentDataSourceModel
	if d := mapAgentResponseToDataSource(agent, &data); d.HasError() {
		t.Fatalf("diags: %+v", d)
	}
	e := decodeEntries(t, data.Multiagent)
	if len(e) != 2 || e[0].Type.ValueString() != "agent" || e[0].ID.ValueString() != "agent_owner" ||
		e[1].Type.ValueString() != "advisor" || e[1].Model.ValueString() != "claude-opus-4-6" {
		t.Fatalf("unexpected roster: %+v", e)
	}

	obj, d := mapAgentToDataSourceObject(agent)
	if d.HasError() {
		t.Fatalf("diags: %+v", d)
	}
	if obj.IsNull() {
		t.Fatal("expected object")
	}

	plain := apiAgentWithMultiagent(t, ``)
	var pdata AgentDataSourceModel
	if d := mapAgentResponseToDataSource(plain, &pdata); d.HasError() {
		t.Fatalf("diags: %+v", d)
	}
	if !pdata.Multiagent.IsNull() {
		t.Fatalf("expected null multiagent, got %v", pdata.Multiagent)
	}
}

func TestBuildMultiagentParams_keepsPinnedVersionOnUpdate(t *testing.T) {
	t.Parallel()
	prior := multiagentObj(t,
		multiagentEntryObj(t, "agent", types.StringValue("agent_a"), types.Int64Value(1), types.StringNull()),
		multiagentEntryObj(t, "agent", types.StringValue("agent_b"), types.Int64Value(5), types.StringNull()),
	)
	// Planned roster reordered: unknown versions for a (unpinned) and c (new member),
	// explicit config version for b.
	planned := multiagentObj(t,
		multiagentEntryObj(t, "agent", types.StringValue("agent_b"), types.Int64Value(9), types.StringNull()),
		multiagentEntryObj(t, "agent", types.StringValue("agent_c"), types.Int64Unknown(), types.StringNull()),
		multiagentEntryObj(t, "agent", types.StringValue("agent_a"), types.Int64Unknown(), types.StringNull()),
	)
	p, diags := buildMultiagentParams(context.Background(), planned, prior)
	if diags.HasError() {
		t.Fatalf("diags: %+v", diags)
	}
	b, _ := json.Marshal(p)
	const want = `{"agents":[{"id":"agent_b","type":"agent","version":9},{"id":"agent_c","type":"agent"},{"id":"agent_a","type":"agent","version":1}],"type":"coordinator"}`
	if string(b) != want {
		t.Fatalf("got  %s\nwant %s", b, want)
	}
}

func TestValidateMultiagentConfig_unknownElement(t *testing.T) {
	t.Parallel()
	list, d := types.ListValue(types.ObjectType{AttrTypes: agentMultiagentEntryAttrTypes}, []attr.Value{
		multiagentEntryObj(t, "agent", types.StringValue("a"), types.Int64Null(), types.StringNull()),
		types.ObjectUnknown(agentMultiagentEntryAttrTypes),
	})
	if d.HasError() {
		t.Fatalf("list: %+v", d)
	}
	obj, d := types.ObjectValue(agentMultiagentAttrTypes, map[string]attr.Value{
		"type": types.StringValue("coordinator"), "agents": list,
		"subagents": types.ObjectNull(agentMultiagentMemberAttrTypes),
		"workflows": types.ObjectNull(agentMultiagentMemberAttrTypes),
		"advisor":   types.ObjectNull(agentMultiagentAdvisorAttrTypes),
	})
	if d.HasError() {
		t.Fatalf("obj: %+v", d)
	}
	if d := validateMultiagentConfig(context.Background(), obj); d.HasError() {
		t.Fatalf("unknown element must be accepted: %+v", d)
	}
}

// multiagent_20261001 configurations, shape from the GET /v1/agents/{id}
// reference example.
const multiagent20261001Roster = `,"multiagent":{
	"type":"multiagent_20261001",
	"advisor":{"type":"enabled","model":"claude-opus-4-6"},
	"subagents":{"type":"enabled","inline_agents":{"type":"enabled"},
		"predefined_agents":[{"type":"agent","id":"agent_a","version":3},{"type":"agent","id":"agent_owner","version":2}]},
	"workflows":{"type":"enabled","inline_agents":{"type":"enabled"},
		"predefined_agents":[{"type":"agent","id":"agent_w","version":1}]}}`

// Import (no configuration): settings holding only defaults stay null, the
// rest is mapped as returned, a `self` entry reading back as the owner's ID.
func TestMapMultiagentToState_multiagent20261001Import(t *testing.T) {
	t.Parallel()
	agent := apiAgentWithMultiagent(t, multiagent20261001Roster)
	got, diags := mapMultiagentToState(context.Background(), agent.Multiagent, agent.ID, types.ObjectNull(agentMultiagentAttrTypes))
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}
	requireMultiagentEqual(t, got, multiagent20261001Obj(t,
		memberObj(t, boolNull, boolNull,
			predefinedObj(t, "agent", types.StringValue("agent_a"), types.Int64Value(3)),
			predefinedObj(t, "agent", types.StringValue("agent_owner"), types.Int64Value(2))),
		memberObj(t, boolNull, boolNull, predefinedObj(t, "agent", types.StringValue("agent_w"), types.Int64Value(1))),
		"claude-opus-4-6"))

	// Only defaults: every setting null.
	agent = apiAgentWithMultiagent(t, `,"multiagent":{"type":"multiagent_20261001","advisor":{"type":"disabled"},
		"subagents":{"type":"enabled","inline_agents":{"type":"enabled"},"predefined_agents":[]},
		"workflows":{"type":"enabled","inline_agents":{"type":"enabled"},"predefined_agents":[]}}`)
	got, diags = mapMultiagentToState(context.Background(), agent.Multiagent, agent.ID, types.ObjectNull(agentMultiagentAttrTypes))
	if diags.HasError() {
		t.Fatalf("diags: %+v", diags)
	}
	requireMultiagentEqual(t, got, multiagent20261001Obj(t, noMember, noMember, ""))
}

// The data sources map every setting as returned, defaults included.
func TestMapAgentResponseToDataSource_multiagent20261001(t *testing.T) {
	t.Parallel()
	agent := apiAgentWithMultiagent(t, multiagent20261001Roster)
	want := multiagent20261001Obj(t,
		memberObj(t, types.BoolValue(true), types.BoolValue(true),
			predefinedObj(t, "agent", types.StringValue("agent_a"), types.Int64Value(3)),
			predefinedObj(t, "agent", types.StringValue("agent_owner"), types.Int64Value(2))),
		memberObj(t, types.BoolValue(true), types.BoolValue(true), predefinedObj(t, "agent", types.StringValue("agent_w"), types.Int64Value(1))),
		"claude-opus-4-6")

	var data AgentDataSourceModel
	if d := mapAgentResponseToDataSource(agent, &data); len(d) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", d)
	}
	requireMultiagentEqual(t, data.Multiagent, want)

	obj, d := mapAgentToDataSourceObject(agent)
	if len(d) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", d)
	}
	listMultiagent, ok := obj.(types.Object).Attributes()["multiagent"].(types.Object)
	if !ok {
		t.Fatal("agents data source: multiagent is not an object")
	}
	requireMultiagentEqual(t, listMultiagent, want)
}

// Disabled settings read back as enabled = false (no inline agents or list),
// and a disabled advisor as null, in the data sources and on import.
func TestMapMultiagentToObject_multiagent20261001Disabled(t *testing.T) {
	t.Parallel()
	agent := apiAgentWithMultiagent(t, `,"multiagent":{"type":"multiagent_20261001",
		"advisor":{"type":"disabled"},"subagents":{"type":"disabled"},"workflows":{"type":"disabled"}}`)
	want := multiagent20261001Obj(t, memberObj(t, types.BoolValue(false), boolNull), memberObj(t, types.BoolValue(false), boolNull), "")

	got, diags := mapMultiagentToObject(agent.Multiagent, agent.ID)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}
	requireMultiagentEqual(t, got, want)

	got, diags = mapMultiagentToState(context.Background(), agent.Multiagent, agent.ID, types.ObjectNull(agentMultiagentAttrTypes))
	if diags.HasError() {
		t.Fatalf("diags: %+v", diags)
	}
	requireMultiagentEqual(t, got, want)
}

// The coordinator branch maps exactly as before, without any warning.
func TestMapMultiagentToObject_coordinatorHasNoWarning(t *testing.T) {
	t.Parallel()
	agent := apiAgentWithMultiagent(t, multiagentAPIRoster)
	got, diags := mapMultiagentToObject(agent.Multiagent, agent.ID)
	if len(diags) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", diags)
	}
	if e := decodeEntries(t, got); len(e) != 4 {
		t.Fatalf("roster = %+v", e)
	}
}

func predefinedObj(t *testing.T, typ string, id types.String, version types.Int64) attr.Value {
	t.Helper()
	v, d := predefinedAgentValue(typ, id, version)
	if d.HasError() {
		t.Fatalf("predefined entry: %+v", d)
	}
	return v
}

// memberObj builds a subagents/workflows setting; no entries leaves
// predefined_agents null.
func memberObj(t *testing.T, enabled, inline types.Bool, entries ...attr.Value) types.Object {
	t.Helper()
	elemType := types.ObjectType{AttrTypes: agentMultiagentPredefinedAgentAttrTypes}
	list := types.ListNull(elemType)
	if len(entries) > 0 {
		var d diag.Diagnostics
		list, d = types.ListValue(elemType, entries)
		if d.HasError() {
			t.Fatalf("predefined list: %+v", d)
		}
	}
	obj, d := types.ObjectValue(agentMultiagentMemberAttrTypes, map[string]attr.Value{
		"enabled": enabled, "inline_agents_enabled": inline, "predefined_agents": list,
	})
	if d.HasError() {
		t.Fatalf("member: %+v", d)
	}
	return obj
}

var noMember = types.ObjectNull(agentMultiagentMemberAttrTypes)

func multiagent20261001Obj(t *testing.T, subagents, workflows types.Object, advisorModel string) types.Object {
	t.Helper()
	advisor := types.ObjectNull(agentMultiagentAdvisorAttrTypes)
	if advisorModel != "" {
		advisor = types.ObjectValueMust(agentMultiagentAdvisorAttrTypes, map[string]attr.Value{"model": types.StringValue(advisorModel)})
	}
	obj, d := types.ObjectValue(agentMultiagentAttrTypes, map[string]attr.Value{
		"type":      types.StringValue("multiagent_20261001"),
		"agents":    types.ListNull(types.ObjectType{AttrTypes: agentMultiagentEntryAttrTypes}),
		"subagents": subagents,
		"workflows": workflows,
		"advisor":   advisor,
	})
	if d.HasError() {
		t.Fatalf("multiagent: %+v", d)
	}
	return obj
}

func requireMultiagentEqual(t *testing.T, got, want types.Object) {
	t.Helper()
	if !got.Equal(want) {
		t.Fatalf("multiagent =\n  %v\nwant\n  %v", got, want)
	}
}

var (
	boolNull  = types.BoolNull()
	int64Null = types.Int64Null()
	strNull   = types.StringNull()
)

// predefined_agents correlate with the configuration by key like the
// coordinator roster (self kept as configured, config order kept), an echoed
// default stays null and the advisor maps back.
func TestMapAgentResponseToState_multiagent20261001Correlation(t *testing.T) {
	t.Parallel()
	agent := apiAgentWithMultiagent(t, `,"multiagent":{"type":"multiagent_20261001",
		"advisor":{"type":"enabled","model":"claude-opus-4-6"},
		"subagents":{"type":"enabled","inline_agents":{"type":"disabled"},
			"predefined_agents":[{"type":"agent","id":"agent_a","version":3},{"type":"agent","id":"agent_owner","version":2}]},
		"workflows":{"type":"enabled","inline_agents":{"type":"enabled"},"predefined_agents":[]}}`)
	data := AgentResourceModel{Multiagent: multiagent20261001Obj(t,
		memberObj(t, boolNull, types.BoolValue(false),
			predefinedObj(t, "self", strNull, int64Null),
			predefinedObj(t, "agent", types.StringValue("agent_a"), types.Int64Unknown())),
		noMember, "claude-opus-4-6")}
	if d := mapAgentResponseToState(context.Background(), agent, &data, readMapping); len(d) != 0 {
		t.Fatalf("unexpected diagnostics: %+v", d)
	}
	requireMultiagentEqual(t, data.Multiagent, multiagent20261001Obj(t,
		memberObj(t, boolNull, types.BoolValue(false),
			predefinedObj(t, "self", strNull, int64Null),
			predefinedObj(t, "agent", types.StringValue("agent_a"), types.Int64Value(3))),
		noMember, "claude-opus-4-6"))
}

// A setting changed out of band surfaces as drift: an unset setting that no
// longer holds the defaults, an explicit value the API no longer has, and an
// advisor added out of band.
func TestMapMultiagentToState_multiagent20261001Drift(t *testing.T) {
	t.Parallel()
	agent := apiAgentWithMultiagent(t, `,"multiagent":{"type":"multiagent_20261001",
		"advisor":{"type":"enabled","model":"claude-opus-4-6"},
		"subagents":{"type":"disabled"},
		"workflows":{"type":"enabled","inline_agents":{"type":"enabled"},"predefined_agents":[{"type":"agent","id":"agent_w","version":1}]}}`)
	prior := multiagent20261001Obj(t, noMember, memberObj(t, types.BoolValue(false), boolNull), "")
	got, diags := mapMultiagentToState(context.Background(), agent.Multiagent, agent.ID, prior)
	if diags.HasError() {
		t.Fatalf("diags: %+v", diags)
	}
	requireMultiagentEqual(t, got, multiagent20261001Obj(t,
		memberObj(t, types.BoolValue(false), boolNull),
		memberObj(t, types.BoolValue(true), boolNull, predefinedObj(t, "agent", types.StringValue("agent_w"), types.Int64Value(1))),
		"claude-opus-4-6"))
}

// A type switched out of band reads as the API type: a coordinator
// configuration reading a multiagent_20261001 agent shows its settings, and
// the reverse maps the roster verbatim.
func TestMapMultiagentToState_typeSwitch(t *testing.T) {
	t.Parallel()
	agent := apiAgentWithMultiagent(t, multiagent20261001Roster)
	prior := multiagentObj(t, multiagentEntryObj(t, "self", types.StringNull(), types.Int64Null(), types.StringNull()))
	got, diags := mapMultiagentToState(context.Background(), agent.Multiagent, agent.ID, prior)
	if diags.HasError() {
		t.Fatalf("diags: %+v", diags)
	}
	var ma agentMultiagentModel
	if d := got.As(context.Background(), &ma, basetypes.ObjectAsOptions{}); d.HasError() {
		t.Fatalf("as: %+v", d)
	}
	if ma.Type.ValueString() != "multiagent_20261001" || !ma.Agents.IsNull() || ma.Subagents.IsNull() {
		t.Fatalf("got %v, want the API's multiagent_20261001 settings", got)
	}

	agent = apiAgentWithMultiagent(t, multiagentAPIRoster)
	got, diags = mapMultiagentToState(context.Background(), agent.Multiagent, agent.ID, multiagent20261001Obj(t, noMember, noMember, ""))
	if diags.HasError() {
		t.Fatalf("diags: %+v", diags)
	}
	if e := decodeEntries(t, got); len(e) != 4 || e[1].ID.ValueString() != "agent_owner" {
		t.Fatalf("roster = %+v, want the API roster verbatim", e)
	}
}

// A type this provider version does not know still reads without an error,
// with a warning naming the agent.
func TestMapMultiagentToObject_unknownTypeWarns(t *testing.T) {
	t.Parallel()
	agent := apiAgentWithMultiagent(t, `,"multiagent":{"type":"multiagent_20270101"}`)
	got, d := mapMultiagentToObject(agent.Multiagent, agent.ID)
	if d.HasError() {
		t.Fatalf("unexpected error: %+v", d)
	}
	w := d.Warnings()
	if len(w) != 1 || w[0].Summary() != "Unsupported multiagent type" ||
		!strings.Contains(w[0].Detail(), `"agent_owner"`) || !strings.Contains(w[0].Detail(), `"multiagent_20270101"`) {
		t.Fatalf("expected one unsupported-type warning naming the agent, got %+v", d)
	}
	var ma agentMultiagentModel
	if d := got.As(context.Background(), &ma, basetypes.ObjectAsOptions{}); d.HasError() || ma.Type.ValueString() != "multiagent_20270101" {
		t.Fatalf("got %v, want the API type kept", got)
	}
}

// Every key is sent, defaults included, so that the API's level-by-level merge
// on update always leaves exactly the planned configuration; an unset version
// reuses the one prior state pins in the same list only.
func TestBuildMultiagentParams_multiagent20261001(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	t.Run("defaults", func(t *testing.T) {
		t.Parallel()
		p, diags := buildMultiagentParams(ctx, multiagent20261001Obj(t, noMember, noMember, ""), types.ObjectNull(agentMultiagentAttrTypes))
		if diags.HasError() {
			t.Fatalf("diags: %+v", diags)
		}
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		assertJSONEqual(t, string(b), `{"type":"multiagent_20261001","advisor":{"type":"disabled"},
			"subagents":{"type":"enabled","inline_agents":{"type":"enabled"},"predefined_agents":[]},
			"workflows":{"type":"enabled","inline_agents":{"type":"enabled"},"predefined_agents":[]}}`)
	})

	t.Run("configured", func(t *testing.T) {
		t.Parallel()
		planned := multiagent20261001Obj(t,
			memberObj(t, boolNull, types.BoolValue(false),
				predefinedObj(t, "agent", types.StringValue("agent_a"), types.Int64Unknown()),
				predefinedObj(t, "self", strNull, types.Int64Unknown()),
				predefinedObj(t, "agent", types.StringValue("agent_b"), types.Int64Value(7))),
			memberObj(t, types.BoolValue(false), boolNull),
			"claude-opus-4-6")
		prior := multiagent20261001Obj(t,
			memberObj(t, boolNull, types.BoolValue(false), predefinedObj(t, "agent", types.StringValue("agent_a"), types.Int64Value(3))),
			memberObj(t, boolNull, boolNull, predefinedObj(t, "agent", types.StringValue("agent_b"), types.Int64Value(5))),
			"")
		p, diags := buildMultiagentParams(ctx, planned, prior)
		if diags.HasError() {
			t.Fatalf("diags: %+v", diags)
		}
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		assertJSONEqual(t, string(b), `{"type":"multiagent_20261001","advisor":{"type":"enabled","model":"claude-opus-4-6"},
			"subagents":{"type":"enabled","inline_agents":{"type":"disabled"},"predefined_agents":[
				{"type":"agent","id":"agent_a","version":3},{"type":"self"},{"type":"agent","id":"agent_b","version":7}]},
			"workflows":{"type":"disabled"}}`)
	})

	t.Run("versions are not reused across types", func(t *testing.T) {
		t.Parallel()
		planned := multiagent20261001Obj(t,
			memberObj(t, boolNull, boolNull, predefinedObj(t, "agent", types.StringValue("agent_a"), types.Int64Unknown())),
			noMember, "")
		prior := multiagentObj(t, multiagentEntryObj(t, "agent", types.StringValue("agent_a"), types.Int64Value(5), types.StringNull()))
		p, diags := buildMultiagentParams(ctx, planned, prior)
		if diags.HasError() {
			t.Fatalf("diags: %+v", diags)
		}
		b, _ := json.Marshal(p.OfMultiagent20261001.Subagents)
		assertJSONEqual(t, string(b), `{"type":"enabled","inline_agents":{"type":"enabled"},"predefined_agents":[{"type":"agent","id":"agent_a"}]}`)
	})
}

func TestValidateMultiagentConfig_multiagent20261001(t *testing.T) {
	t.Parallel()
	id := types.StringValue
	agentA := predefinedObj(t, "agent", id("a"), int64Null)
	self := predefinedObj(t, "self", strNull, int64Null)
	withSubagents := func(m types.Object) types.Object { return multiagent20261001Obj(t, m, noMember, "") }

	tests := []struct {
		name     string
		obj      types.Object
		wantPath string // empty: valid
	}{
		{"defaults", multiagent20261001Obj(t, noMember, noMember, ""), ""},
		{"full", multiagent20261001Obj(t,
			memberObj(t, types.BoolValue(true), types.BoolValue(false), agentA, self),
			memberObj(t, types.BoolValue(false), boolNull), "m"), ""},
		{"with a coordinator roster", func() types.Object {
			attrs := multiagent20261001Obj(t, noMember, noMember, "").Attributes()
			attrs["agents"] = multiagentObj(t, multiagentEntryObj(t, "self", strNull, int64Null, strNull)).Attributes()["agents"]
			return types.ObjectValueMust(agentMultiagentAttrTypes, attrs)
		}(), "multiagent.agents"},
		{"disabled with inline agents", withSubagents(memberObj(t, types.BoolValue(false), types.BoolValue(true))),
			"multiagent.subagents.inline_agents_enabled"},
		{"disabled with predefined agents", withSubagents(memberObj(t, types.BoolValue(false), boolNull, agentA)),
			"multiagent.subagents.predefined_agents"},
		{"inline off without predefined agents", multiagent20261001Obj(t, noMember, memberObj(t, boolNull, types.BoolValue(false)), ""),
			"multiagent.workflows.predefined_agents"},
		{"inline off with unknown predefined agents", withSubagents(types.ObjectValueMust(agentMultiagentMemberAttrTypes, map[string]attr.Value{
			"enabled": boolNull, "inline_agents_enabled": types.BoolValue(false),
			"predefined_agents": types.ListUnknown(types.ObjectType{AttrTypes: agentMultiagentPredefinedAgentAttrTypes}),
		})), ""},
		{"agent without id", withSubagents(memberObj(t, boolNull, boolNull, predefinedObj(t, "agent", strNull, int64Null))),
			"multiagent.subagents.predefined_agents[0].id"},
		{"agent with unknown id", withSubagents(memberObj(t, boolNull, boolNull, predefinedObj(t, "agent", types.StringUnknown(), int64Null))), ""},
		{"self with id", withSubagents(memberObj(t, boolNull, boolNull, predefinedObj(t, "self", id("a"), int64Null))),
			"multiagent.subagents.predefined_agents[0].id"},
		{"self with version", withSubagents(memberObj(t, boolNull, boolNull, predefinedObj(t, "self", strNull, types.Int64Value(1)))),
			"multiagent.subagents.predefined_agents[0].version"},
		{"duplicate agents", withSubagents(memberObj(t, boolNull, boolNull, agentA, agentA)), "multiagent.subagents.predefined_agents"},
		{"two selfs", withSubagents(memberObj(t, boolNull, boolNull, self, self)), "multiagent.subagents.predefined_agents"},
		{"same agent in both lists", multiagent20261001Obj(t,
			memberObj(t, boolNull, boolNull, agentA), memberObj(t, boolNull, boolNull, agentA), ""), ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			d := validateMultiagentConfig(context.Background(), tc.obj)
			if tc.wantPath == "" {
				if d.HasError() {
					t.Fatalf("unexpected diags: %+v", d)
				}
				return
			}
			if d.ErrorsCount() != 1 {
				t.Fatalf("got %d errors, want 1: %+v", d.ErrorsCount(), d)
			}
			withPath, ok := d.Errors()[0].(interface{ Path() path.Path })
			if !ok || withPath.Path().String() != tc.wantPath {
				t.Errorf("error = %+v, want it on %s", d.Errors()[0], tc.wantPath)
			}
		})
	}

	// The coordinator type requires its roster and rejects the settings of the
	// other type.
	t.Run("coordinator", func(t *testing.T) {
		t.Parallel()
		attrs := multiagentObj(t, multiagentEntryObj(t, "self", strNull, int64Null, strNull)).Attributes()
		attrs["agents"] = types.ListNull(types.ObjectType{AttrTypes: agentMultiagentEntryAttrTypes})
		attrs["advisor"] = types.ObjectValueMust(agentMultiagentAdvisorAttrTypes, map[string]attr.Value{"model": types.StringValue("m")})
		d := validateMultiagentConfig(context.Background(), types.ObjectValueMust(agentMultiagentAttrTypes, attrs))
		var paths []string
		for _, e := range d.Errors() {
			if withPath, ok := e.(interface{ Path() path.Path }); ok {
				paths = append(paths, withPath.Path().String())
			}
		}
		if strings.Join(paths, ",") != "multiagent.agents,multiagent.advisor" {
			t.Errorf("error paths = %v, want multiagent.agents and multiagent.advisor", paths)
		}
	})
}
