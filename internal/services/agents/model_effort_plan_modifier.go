// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// modelEffortFollowsModel returns a plan modifier for the Computed
// `model_effort` attribute. When the configuration leaves it unset, the stored
// value is reused only while `model` is unchanged; if `model` changes the plan
// stays unknown, because the API then resolves the new model's default (it keeps
// an omitted effort only for an unchanged model id).
func modelEffortFollowsModel() planmodifier.String {
	return modelEffortFollowsModelModifier{}
}

type modelEffortFollowsModelModifier struct{}

func (modelEffortFollowsModelModifier) Description(context.Context) string {
	return "Keeps the stored effort while `model` is unchanged; leaves it unknown when `model` changes."
}

func (m modelEffortFollowsModelModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (modelEffortFollowsModelModifier) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	// Config wins; nothing to do on create, destroy or when already known.
	if !req.ConfigValue.IsNull() || !req.PlanValue.IsUnknown() || req.State.Raw.IsNull() {
		return
	}

	var stateModel, planModel types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("model"), &stateModel)...)
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("model"), &planModel)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !planModel.IsUnknown() && stateModel.Equal(planModel) {
		resp.PlanValue = req.StateValue
	}
}
