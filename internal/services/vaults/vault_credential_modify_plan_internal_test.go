// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package vaults

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-go/tftypes"

	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
)

// TestVaultCredentialModifyPlan_updatedAt covers the plan Terraform builds
// for an environment_variable credential configured without an
// injection_location block: the framework applies the defaults of
// injection_location.header and .body, sees a change and plans updated_at as
// unknown, then UseNonNullStateForUnknown restores the stored injection_location.
// That plan changes nothing but updated_at, so the resource's ModifyPlan
// must turn it back into an empty plan, or every plan shows an in-place
// update that changes nothing. A real change keeps updated_at unknown, and a
// create is left alone.
func TestVaultCredentialModifyPlan_updatedAt(t *testing.T) {
	t.Parallel()

	r := NewVaultCredentialResource()
	sch := schematest.ResourceSchema(t, r)
	objType := schematest.ResourceObjectType(t, r)
	networkingType := objType.AttributeTypes["networking"].(tftypes.Object)
	locationType := objType.AttributeTypes["injection_location"].(tftypes.Object)
	const storedUpdatedAt = "2026-01-01T00:00:02Z"

	// credential is an environment_variable credential with both locations,
	// as stored after a create without an injection_location block.
	credential := func(header, body bool, updatedAt tftypes.Value) tftypes.Value {
		vals := schematest.NullValues(t, r)
		vals["id"] = tftypes.NewValue(tftypes.String, "vcrd_01")
		vals["vault_id"] = tftypes.NewValue(tftypes.String, "vlt_01")
		vals["type"] = tftypes.NewValue(tftypes.String, "environment_variable")
		vals["secret_name"] = tftypes.NewValue(tftypes.String, "API_KEY")
		vals["token_wo_version"] = tftypes.NewValue(tftypes.Number, 1)
		vals["networking"] = tftypes.NewValue(networkingType, map[string]tftypes.Value{
			"mode":          tftypes.NewValue(tftypes.String, "unrestricted"),
			"allowed_hosts": tftypes.NewValue(networkingType.AttributeTypes["allowed_hosts"], nil),
		})
		vals["injection_location"] = tftypes.NewValue(locationType, map[string]tftypes.Value{
			"header": tftypes.NewValue(tftypes.Bool, header),
			"body":   tftypes.NewValue(tftypes.Bool, body),
		})
		vals["created_at"] = tftypes.NewValue(tftypes.String, "2026-01-01T00:00:01Z")
		vals["updated_at"] = updatedAt
		vals["credential_type"] = tftypes.NewValue(tftypes.String, "vault_credential")
		return tftypes.NewValue(objType, vals)
	}
	stored := tftypes.NewValue(tftypes.String, storedUpdatedAt)
	unknown := tftypes.NewValue(tftypes.String, tftypes.UnknownValue)

	tests := []struct {
		name          string
		state         tftypes.Value
		plan          tftypes.Value
		wantUpdatedAt types.String
	}{
		{
			name:          "no change but updated_at keeps the stored value",
			state:         credential(true, true, stored),
			plan:          credential(true, true, unknown),
			wantUpdatedAt: types.StringValue(storedUpdatedAt),
		},
		{
			name:          "a narrowed injection_location keeps updated_at unknown",
			state:         credential(true, true, stored),
			plan:          credential(true, false, unknown),
			wantUpdatedAt: types.StringUnknown(),
		},
		{
			name:          "a create is left alone",
			state:         tftypes.NewValue(objType, nil),
			plan:          credential(true, true, unknown),
			wantUpdatedAt: types.StringUnknown(),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			req := resource.ModifyPlanRequest{
				State: tfsdk.State{Schema: sch, Raw: tc.state},
				Plan:  tfsdk.Plan{Schema: sch, Raw: tc.plan},
			}
			resp := resource.ModifyPlanResponse{Plan: req.Plan}
			// The framework calls ModifyPlan only on a resource that implements it.
			if m, ok := r.(resource.ResourceWithModifyPlan); ok {
				m.ModifyPlan(context.Background(), req, &resp)
			}
			if resp.Diagnostics.HasError() {
				t.Fatalf("diags: %v", resp.Diagnostics)
			}

			var got types.String
			if d := resp.Plan.GetAttribute(context.Background(), path.Root("updated_at"), &got); d.HasError() {
				t.Fatalf("updated_at: %v", d)
			}
			if !got.Equal(tc.wantUpdatedAt) {
				t.Errorf("planned updated_at = %v, want %v", got, tc.wantUpdatedAt)
			}
			if tc.wantUpdatedAt.IsUnknown() && !resp.Plan.Raw.Equal(tc.plan) {
				t.Errorf("plan changed:\n got %v\nwant %v", resp.Plan.Raw, tc.plan)
			}
			if !tc.wantUpdatedAt.IsUnknown() && !resp.Plan.Raw.Equal(tc.state) {
				t.Errorf("plan is not the state, so Terraform still plans an update:\n got %v\nwant %v", resp.Plan.Raw, tc.state)
			}
		})
	}
}

// TestVaultCredentialModifyPlan_unreadInjectionLocation covers an
// environment_variable credential whose state was written before
// injection_location existed (null) and that is planned without a refresh
// (-refresh=false), with no injection_location block. The attribute stays
// unknown so that Update can store the location the API returns. With no other
// change, the plan must still be empty; with one, injection_location stays
// unknown.
func TestVaultCredentialModifyPlan_unreadInjectionLocation(t *testing.T) {
	t.Parallel()

	r := NewVaultCredentialResource()
	sch := schematest.ResourceSchema(t, r)
	objType := schematest.ResourceObjectType(t, r)
	locationType := objType.AttributeTypes["injection_location"].(tftypes.Object)
	const storedUpdatedAt = "2026-01-01T00:00:02Z"

	credential := func(displayName string, location, updatedAt tftypes.Value) tftypes.Value {
		vals := schematest.NullValues(t, r)
		vals["id"] = tftypes.NewValue(tftypes.String, "vcrd_01")
		vals["vault_id"] = tftypes.NewValue(tftypes.String, "vlt_01")
		vals["type"] = tftypes.NewValue(tftypes.String, "environment_variable")
		vals["secret_name"] = tftypes.NewValue(tftypes.String, "API_KEY")
		vals["display_name"] = tftypes.NewValue(tftypes.String, displayName)
		vals["injection_location"] = location
		vals["updated_at"] = updatedAt
		return tftypes.NewValue(objType, vals)
	}
	nullLocation := tftypes.NewValue(locationType, nil)
	unknownLocation := tftypes.NewValue(locationType, tftypes.UnknownValue)
	stored := tftypes.NewValue(tftypes.String, storedUpdatedAt)
	unknown := tftypes.NewValue(tftypes.String, tftypes.UnknownValue)
	state := credential("old", nullLocation, stored)

	tests := []struct {
		name string
		plan tftypes.Value
		want tftypes.Value
	}{
		{"no other change plans nothing", credential("old", unknownLocation, unknown), state},
		{"another change keeps injection_location unknown", credential("new", unknownLocation, unknown), credential("new", unknownLocation, unknown)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			config := schematest.NullValues(t, r)
			config["type"] = tftypes.NewValue(tftypes.String, "environment_variable")
			req := resource.ModifyPlanRequest{
				Config: tfsdk.Config{Schema: sch, Raw: tftypes.NewValue(objType, config)},
				State:  tfsdk.State{Schema: sch, Raw: state},
				Plan:   tfsdk.Plan{Schema: sch, Raw: tc.plan},
			}
			resp := resource.ModifyPlanResponse{Plan: req.Plan}
			if m, ok := r.(resource.ResourceWithModifyPlan); ok {
				m.ModifyPlan(context.Background(), req, &resp)
			}
			if resp.Diagnostics.HasError() {
				t.Fatalf("diags: %v", resp.Diagnostics)
			}
			if !resp.Plan.Raw.Equal(tc.want) {
				t.Errorf("plan:\n got %v\nwant %v", resp.Plan.Raw, tc.want)
			}
		})
	}
}
