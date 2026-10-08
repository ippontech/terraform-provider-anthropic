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
	p, diags := buildMultiagentParams(context.Background(), obj)
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
	p, diags := buildMultiagentParams(context.Background(), types.ObjectNull(agentMultiagentAttrTypes))
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
		if d := mapAgentResponseToState(context.Background(), agent, &data); d.HasError() {
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
