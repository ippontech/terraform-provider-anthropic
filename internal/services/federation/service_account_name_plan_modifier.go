// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// serviceAccountNameFollowsID returns a plan modifier for the Computed
// target.service_account_name. It reuses the stored name only while the
// sibling service_account_id is unchanged, and leaves it unknown when the
// rule moves to another service account. UseStateForUnknown kept the old
// account's name in the plan, which the API's answer (the new account's
// name) then contradicted: "inconsistent result after apply".
func serviceAccountNameFollowsID() planmodifier.String {
	return serviceAccountNameFollowsIDModifier{}
}

type serviceAccountNameFollowsIDModifier struct{}

func (serviceAccountNameFollowsIDModifier) Description(context.Context) string {
	return "Keeps the stored service account name while `service_account_id` is unchanged; leaves it unknown when it changes."
}

func (m serviceAccountNameFollowsIDModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (serviceAccountNameFollowsIDModifier) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	// Nothing to do on create or destroy, or when the value is already known.
	if !req.PlanValue.IsUnknown() || req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}

	idPath := req.Path.ParentPath().AtName("service_account_id")
	var stateID, planID types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, idPath, &stateID)...)
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, idPath, &planID)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !planID.IsUnknown() && !stateID.IsNull() && planID.Equal(stateID) {
		resp.PlanValue = req.StateValue
	}
}
