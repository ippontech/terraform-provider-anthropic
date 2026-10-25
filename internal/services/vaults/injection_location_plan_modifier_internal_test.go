// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package vaults

import (
	"context"
	"testing"

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
