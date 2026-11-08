// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package vaults

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
)

func TestInjectionLocationNullUnlessEnvVar(t *testing.T) {
	t.Parallel()

	r := NewVaultCredentialResource()
	objType := schematest.ResourceObjectType(t, r)
	sch := schematest.ResourceSchema(t, r)
	plan := func(authType string) tfsdk.Plan {
		vals := schematest.NullValues(t, r)
		vals["type"] = tftypes.NewValue(tftypes.String, authType)
		return tfsdk.Plan{Schema: sch, Raw: tftypes.NewValue(objType, vals)}
	}
	unknown := types.ObjectUnknown(credentialInjectionLocationAttrTypes)
	configured := injectionLocationObj(types.BoolValue(true), types.BoolValue(false))

	tests := []struct {
		name     string
		authType string
		config   types.Object
		want     types.Object
	}{
		{"other type plans null", "static_bearer", types.ObjectNull(credentialInjectionLocationAttrTypes), types.ObjectNull(credentialInjectionLocationAttrTypes)},
		{"environment_variable stays unknown", "environment_variable", types.ObjectNull(credentialInjectionLocationAttrTypes), unknown},
		{"configured value is left alone", "static_bearer", configured, unknown},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := planmodifier.ObjectRequest{ConfigValue: tc.config, PlanValue: unknown, Plan: plan(tc.authType)}
			resp := planmodifier.ObjectResponse{PlanValue: req.PlanValue}
			injectionLocationNullUnlessEnvVar().PlanModifyObject(context.Background(), req, &resp)
			if resp.Diagnostics.HasError() {
				t.Fatalf("diags: %v", resp.Diagnostics)
			}
			if !resp.PlanValue.Equal(tc.want) {
				t.Errorf("plan = %v, want %v", resp.PlanValue, tc.want)
			}
		})
	}
}

// TestInjectionLocationPlanModifiers_update runs the attribute's whole
// modifier chain, in schema order, on an update without an
// injection_location block. State written before the attribute existed holds
// null: planning that null for an environment_variable credential would fail
// the apply ("Provider produced inconsistent result after apply") once Update
// stores the location the API returns, so it must stay unknown. A stored value
// is kept, and other types still plan null.
func TestInjectionLocationPlanModifiers_update(t *testing.T) {
	t.Parallel()

	r := NewVaultCredentialResource()
	sch := schematest.ResourceSchema(t, r)
	objType := schematest.ResourceObjectType(t, r)
	attr, ok := sch.Attributes["injection_location"].(schema.SingleNestedAttribute)
	if !ok {
		t.Fatal("injection_location is not a single nested attribute")
	}
	null := types.ObjectNull(credentialInjectionLocationAttrTypes)
	unknown := types.ObjectUnknown(credentialInjectionLocationAttrTypes)
	stored := injectionLocationObj(types.BoolValue(true), types.BoolValue(false))

	tests := []struct {
		name     string
		authType string
		state    types.Object
		want     types.Object
	}{
		{"environment_variable with no stored value stays unknown", "environment_variable", null, unknown},
		{"environment_variable keeps the stored value", "environment_variable", stored, stored},
		{"other type plans null", "static_bearer", null, null},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			stateLoc, err := tc.state.ToTerraformValue(ctx)
			if err != nil {
				t.Fatal(err)
			}
			vals := schematest.NullValues(t, r)
			vals["id"] = tftypes.NewValue(tftypes.String, "vcrd_01")
			vals["type"] = tftypes.NewValue(tftypes.String, tc.authType)
			vals["injection_location"] = stateLoc
			req := planmodifier.ObjectRequest{
				Path:        path.Root("injection_location"),
				ConfigValue: null,
				PlanValue:   unknown,
				StateValue:  tc.state,
				Plan:        tfsdk.Plan{Schema: sch, Raw: tftypes.NewValue(objType, vals)},
				State:       tfsdk.State{Schema: sch, Raw: tftypes.NewValue(objType, vals)},
			}
			for _, m := range attr.ObjectPlanModifiers() {
				resp := planmodifier.ObjectResponse{PlanValue: req.PlanValue}
				m.PlanModifyObject(ctx, req, &resp)
				if resp.Diagnostics.HasError() {
					t.Fatalf("diags: %v", resp.Diagnostics)
				}
				req.PlanValue = resp.PlanValue
			}
			if !req.PlanValue.Equal(tc.want) {
				t.Errorf("plan = %v, want %v", req.PlanValue, tc.want)
			}
		})
	}
}
