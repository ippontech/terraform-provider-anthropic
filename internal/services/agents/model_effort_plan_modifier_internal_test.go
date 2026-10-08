// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
)

func TestModelEffortFollowsModel(t *testing.T) {
	t.Parallel()

	r := NewAgentResource()
	objType := schematest.ResourceObjectType(t, r)
	sch := schematest.ResourceSchema(t, r)

	raw := func(model string, effort tftypes.Value) tftypes.Value {
		vals := schematest.NullValues(t, r)
		vals["model"] = tftypes.NewValue(tftypes.String, model)
		vals["model_effort"] = effort
		return tftypes.NewValue(objType, vals)
	}
	str := func(s string) tftypes.Value { return tftypes.NewValue(tftypes.String, s) }
	unknown := tftypes.NewValue(tftypes.String, tftypes.UnknownValue)

	tests := []struct {
		name       string
		state      tftypes.Value
		planModel  string
		planEffort tftypes.Value
		config     types.String
		want       types.String
	}{
		{
			name:       "same model reuses the state value",
			state:      raw("claude-sonnet-4-6", str("high")),
			planModel:  "claude-sonnet-4-6",
			planEffort: unknown,
			config:     types.StringNull(),
			want:       types.StringValue("high"),
		},
		{
			name:       "changed model leaves it unknown",
			state:      raw("claude-sonnet-4-6", str("high")),
			planModel:  "claude-opus-4-6",
			planEffort: unknown,
			config:     types.StringNull(),
			want:       types.StringUnknown(),
		},
		{
			name:       "config wins over the state value",
			state:      raw("claude-sonnet-4-6", str("high")),
			planModel:  "claude-sonnet-4-6",
			planEffort: str("low"),
			config:     types.StringValue("low"),
			want:       types.StringValue("low"),
		},
		{
			name:       "create leaves it unknown",
			state:      tftypes.NewValue(objType, nil),
			planModel:  "claude-sonnet-4-6",
			planEffort: unknown,
			config:     types.StringNull(),
			want:       types.StringUnknown(),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			state := tfsdk.State{Raw: tt.state, Schema: sch}
			plan := tfsdk.Plan{Raw: raw(tt.planModel, tt.planEffort), Schema: sch}

			var stateValue types.String
			if !tt.state.IsNull() {
				stateValue = types.StringValue("high")
			} else {
				stateValue = types.StringNull()
			}
			planValue := types.StringUnknown()
			if tt.planEffort.IsKnown() {
				planValue = types.StringValue("low")
			}

			req := planmodifier.StringRequest{
				State:       state,
				Plan:        plan,
				StateValue:  stateValue,
				PlanValue:   planValue,
				ConfigValue: tt.config,
			}
			resp := &planmodifier.StringResponse{PlanValue: planValue}
			modelEffortFollowsModel().PlanModifyString(context.Background(), req, resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("diags: %+v", resp.Diagnostics)
			}
			if !resp.PlanValue.Equal(tt.want) {
				t.Errorf("got %v, want %v", resp.PlanValue, tt.want)
			}
		})
	}
}
