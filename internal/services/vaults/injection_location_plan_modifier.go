// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package vaults

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// injectionLocationNullUnlessEnvVar returns a plan modifier for the Optional +
// Computed `injection_location` attribute. Only environment_variable
// credentials have one, so for any other known `type` an unset value is planned
// as null instead of "known after apply" (UseNonNullStateForUnknown leaves it
// unknown whenever the prior state is null, i.e. on create and for every
// credential of another type). An environment_variable credential stays
// unknown, so Update can store the location the API returns.
func injectionLocationNullUnlessEnvVar() planmodifier.Object {
	return injectionLocationNullUnlessEnvVarModifier{}
}

type injectionLocationNullUnlessEnvVarModifier struct{}

func (injectionLocationNullUnlessEnvVarModifier) Description(context.Context) string {
	return "Plans null when `type` is not `environment_variable` and the attribute is not configured."
}

func (m injectionLocationNullUnlessEnvVarModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (injectionLocationNullUnlessEnvVarModifier) PlanModifyObject(ctx context.Context, req planmodifier.ObjectRequest, resp *planmodifier.ObjectResponse) {
	if !req.ConfigValue.IsNull() || !req.PlanValue.IsUnknown() || req.Plan.Raw.IsNull() {
		return
	}

	var authType types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("type"), &authType)...)
	if resp.Diagnostics.HasError() || authType.IsNull() || authType.IsUnknown() {
		return
	}
	if authType.ValueString() != "environment_variable" {
		resp.PlanValue = types.ObjectNull(credentialInjectionLocationAttrTypes)
	}
}
