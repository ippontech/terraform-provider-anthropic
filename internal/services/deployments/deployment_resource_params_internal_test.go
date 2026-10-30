// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package deployments

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
)

const testEvents = `[{"type":"user.message","content":[{"type":"text","text":"Go."}]}]`

// requiredModel is a planned deployment with the required attributes set
// and every optional one null.
func requiredModel() DeploymentResourceModel {
	return DeploymentResourceModel{
		Name:          types.StringValue("nightly"),
		AgentID:       types.StringValue("agent_1"),
		EnvironmentID: types.StringValue("env_1"),
		InitialEvents: jsontypes.NewNormalizedValue(testEvents),
		Description:   types.StringNull(),
		Metadata:      types.MapNull(types.StringType),
		Resources:     jsontypes.NewNormalizedNull(),
		Budget:        jsontypes.NewNormalizedNull(),
		VaultIDs:      types.ListNull(types.StringType),
		Schedule:      types.ObjectNull(deploymentScheduleAttrTypes),
	}
}

// fullModel is requiredModel with every optional attribute set.
func fullModel() DeploymentResourceModel {
	m := requiredModel()
	m.Description = types.StringValue("Nightly.")
	m.Metadata = types.MapValueMust(types.StringType, map[string]attr.Value{"team": types.StringValue("data")})
	m.Resources = jsontypes.NewNormalizedValue(`[{"type":"memory_store","memory_store_id":"memstore_1"}]`)
	m.Budget = jsontypes.NewNormalizedValue(`{"type":"limit","max_list_cost":{"amount":"2500","currency":"USD"}}`)
	m.VaultIDs = types.ListValueMust(types.StringType, []attr.Value{types.StringValue("vlt_1")})
	m.Schedule = types.ObjectValueMust(deploymentScheduleAttrTypes, map[string]attr.Value{
		"expression": types.StringValue("0 9 * * *"), "timezone": types.StringValue("UTC"),
	})
	return m
}

// unknownModel is requiredModel with every optional attribute unknown.
func unknownModel() DeploymentResourceModel {
	m := requiredModel()
	m.Description = types.StringUnknown()
	m.Metadata = types.MapUnknown(types.StringType)
	m.Resources = jsontypes.NewNormalizedUnknown()
	m.Budget = jsontypes.NewNormalizedUnknown()
	m.VaultIDs = types.ListUnknown(types.StringType)
	m.Schedule = types.ObjectUnknown(deploymentScheduleAttrTypes)
	return m
}

const (
	requiredBody = `"name":"nightly","agent":"agent_1","environment_id":"env_1","initial_events":` + testEvents
	optionalBody = `"description":"Nightly.","metadata":{"team":"data"},` +
		`"resources":[{"type":"memory_store","memory_store_id":"memstore_1"}],` +
		`"budget":{"type":"limit","max_list_cost":{"amount":"2500","currency":"USD"}},` +
		`"vault_ids":["vlt_1"],"schedule":{"expression":"0 9 * * *","timezone":"UTC","type":"cron"}`
)

func requireBody(t *testing.T, params any, want string) {
	t.Helper()
	got, err := json.Marshal(params)
	if err != nil {
		t.Fatal(err)
	}
	if diff := sdktest.JSONDiff(string(got), want); diff != "" {
		t.Errorf("body %s differs from %s:\n%s", got, want, diff)
	}
}

// TestBuildDeploymentCreateParams pins the create body: the required
// attributes always, each optional one when known, empty collections as
// such.
func TestBuildDeploymentCreateParams(t *testing.T) {
	t.Parallel()
	empty := requiredModel()
	empty.Description = types.StringValue("")
	empty.Metadata = types.MapValueMust(types.StringType, map[string]attr.Value{})
	empty.VaultIDs = types.ListValueMust(types.StringType, []attr.Value{})
	for name, tc := range map[string]struct {
		model DeploymentResourceModel
		want  string
	}{
		"required only":     {requiredModel(), `{` + requiredBody + `}`},
		"optional unknown":  {unknownModel(), `{` + requiredBody + `}`},
		"every attribute":   {fullModel(), `{` + requiredBody + `,` + optionalBody + `}`},
		"empty collections": {empty, `{` + requiredBody + `,"description":"","metadata":{},"vault_ids":[]}`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			params, diags := buildDeploymentCreateParams(context.Background(), &tc.model)
			if diags.HasError() {
				t.Fatalf("diagnostics: %v", diags)
			}
			requireBody(t, params, tc.want)
		})
	}
}

// TestBuildDeploymentUpdateParams pins the update body: every known
// attribute is sent (lists and JSON as full replacements), a null
// description as null, an unknown one left out, and metadata as a patch
// against the prior state.
func TestBuildDeploymentUpdateParams(t *testing.T) {
	t.Parallel()
	withState := fullModel()
	withState.Metadata = types.MapValueMust(types.StringType, map[string]attr.Value{
		"team": types.StringValue("data"), "tier": types.StringValue("gold"),
	})
	nullRequired := requiredModel()
	nullRequired.Name, nullRequired.AgentID = types.StringNull(), types.StringUnknown()
	nullRequired.EnvironmentID, nullRequired.InitialEvents = types.StringUnknown(), jsontypes.NewNormalizedNull()
	for name, tc := range map[string]struct {
		plan, state DeploymentResourceModel
		want        string
	}{
		"required only":    {requiredModel(), requiredModel(), `{` + requiredBody + `,"description":null}`},
		"optional unknown": {unknownModel(), requiredModel(), `{` + requiredBody + `}`},
		"every attribute":  {fullModel(), requiredModel(), `{` + requiredBody + `,` + optionalBody + `}`},
		"metadata key removed": {
			fullModel(), withState,
			`{` + requiredBody + `,` + optionalBody[:len(`"description":"Nightly.",`)] + `"metadata":{"team":"data","tier":null},` +
				optionalBody[len(`"description":"Nightly.","metadata":{"team":"data"},`):] + `}`,
		},
		"null and unknown required attributes": {nullRequired, requiredModel(), `{"description":null}`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			params, diags := buildDeploymentUpdateParams(context.Background(), &tc.plan, &tc.state)
			if diags.HasError() {
				t.Fatalf("diagnostics: %v", diags)
			}
			requireBody(t, params, tc.want)
		})
	}
}

// TestBuildDeploymentParams_errors: initial_events or resources of the wrong
// shape is an error diagnostic naming the attribute, with no params.
func TestBuildDeploymentParams_errors(t *testing.T) {
	t.Parallel()
	badEvents := fullModel()
	badEvents.InitialEvents = jsontypes.NewNormalizedValue(`{}`)
	badResources := fullModel()
	badResources.Resources = jsontypes.NewNormalizedValue(`[{}]`)
	for name, tc := range map[string]struct {
		model   DeploymentResourceModel
		summary string
	}{
		"initial_events": {badEvents, "Invalid initial_events"},
		"resources":      {badResources, "Invalid resources"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			params, diags := buildDeploymentCreateParams(context.Background(), &tc.model)
			if !diags.HasError() || diags.Errors()[0].Summary() != tc.summary {
				t.Errorf("create diagnostics = %v, want %q", diags, tc.summary)
			}
			requireBody(t, params, `{"environment_id":"","name":""}`)
			state := requiredModel()
			update, diags := buildDeploymentUpdateParams(context.Background(), &tc.model, &state)
			if !diags.HasError() || diags.Errors()[0].Summary() != tc.summary {
				t.Errorf("update diagnostics = %v, want %q", diags, tc.summary)
			}
			requireBody(t, update, `{}`)
		})
	}
}
