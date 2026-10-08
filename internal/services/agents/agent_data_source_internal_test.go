// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents

import (
	"encoding/json"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

func parseAgentFixture(t *testing.T, model string) *anthropic.BetaManagedAgentsAgent {
	t.Helper()
	var agent anthropic.BetaManagedAgentsAgent
	raw := `{"id":"agent_1","name":"n","model":` + model + `,"version":1,"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`
	if err := json.Unmarshal([]byte(raw), &agent); err != nil {
		t.Fatalf("unmarshal: %s", err)
	}
	return &agent
}

func TestMapAgentResponseToDataSource_modelEffortAndGeo(t *testing.T) {
	t.Parallel()

	var present AgentDataSourceModel
	agent := parseAgentFixture(t, `{"id":"claude-sonnet-4-6","effort":{"type":"max"},"inference_geo":"global"}`)
	if diags := mapAgentResponseToDataSource(agent, &present); diags.HasError() {
		t.Fatalf("diags: %+v", diags)
	}
	if present.ModelEffort.ValueString() != "max" || present.ModelInferenceGeo.ValueString() != "global" {
		t.Errorf("got effort=%v geo=%v", present.ModelEffort, present.ModelInferenceGeo)
	}

	var absent AgentDataSourceModel
	if diags := mapAgentResponseToDataSource(parseAgentFixture(t, `{"id":"claude-sonnet-4-6"}`), &absent); diags.HasError() {
		t.Fatalf("diags: %+v", diags)
	}
	if !absent.ModelEffort.IsNull() || !absent.ModelInferenceGeo.IsNull() {
		t.Errorf("got effort=%v geo=%v, want null", absent.ModelEffort, absent.ModelInferenceGeo)
	}
}

func TestMapAgentToDataSourceObject_modelEffortAndGeo(t *testing.T) {
	t.Parallel()

	obj := func(model string) map[string]attr.Value {
		v, diags := mapAgentToDataSourceObject(parseAgentFixture(t, model))
		if diags.HasError() {
			t.Fatalf("diags: %+v", diags)
		}
		return v.(basetypes.ObjectValue).Attributes()
	}

	got := obj(`{"id":"claude-sonnet-4-6","effort":{"type":"low"},"inference_geo":"us"}`)
	if got["model_effort"].String() != `"low"` || got["model_inference_geo"].String() != `"us"` {
		t.Errorf("got effort=%v geo=%v", got["model_effort"], got["model_inference_geo"])
	}
	got = obj(`{"id":"claude-sonnet-4-6"}`)
	if !got["model_effort"].IsNull() || !got["model_inference_geo"].IsNull() {
		t.Errorf("got effort=%v geo=%v, want null", got["model_effort"], got["model_inference_geo"])
	}
}
