// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents

import (
	"context"
	"fmt"
	"regexp"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	dsschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
)

// The execution identity (SDK v1.81.0+) is the principal an agent's runs act
// as: the Anthropic-managed service account (the default, no identifier) or an
// AWS IAM role on Claude Platform on AWS
// (https://platform.claude.com/docs/en/api/beta/agents/create,
// https://platform.claude.com/docs/en/managed-agents/scheduled-deployments).
const (
	executionIdentityServiceAccount = "service_account"
	executionIdentityAWSRole        = "aws_role"
)

type agentExecutionIdentityModel struct {
	Type    types.String `tfsdk:"type"`
	RoleARN types.String `tfsdk:"role_arn"`
}

var agentExecutionIdentityAttrTypes = map[string]attr.Type{
	"type":     types.StringType,
	"role_arn": types.StringType,
}

// executionIdentityRoleARN accepts an IAM role ARN in any partition, with or
// without a path.
var executionIdentityRoleARN = regexp.MustCompile(`^arn:[a-z-]+:iam::[0-9]{12}:role/.+$`)

func executionIdentitySchema() schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Optional: true,
		MarkdownDescription: "Principal the agent's runs act as. When omitted, runs use the Anthropic-managed service account (the API default), " +
			"and removing the attribute restores that default on the next update. " +
			"On Claude Platform on AWS, an `aws_role` is required before an `anthropic_deployment` can use the agent: " +
			"see the Execution identity section for the role's trust policy and permissions.",
		Attributes: map[string]schema.Attribute{
			"type": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "`service_account` (the Anthropic-managed service account, which carries no identifier) or `aws_role` (an IAM role in your AWS account).",
				Validators:          []validator.String{stringvalidator.OneOf(executionIdentityServiceAccount, executionIdentityAWSRole)},
			},
			"role_arn": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "ARN of the IAM role, e.g. `arn:aws:iam::123456789012:role/agent-runner`. " +
					"Required when `type` is `aws_role`, forbidden otherwise. Up to 2048 characters.",
				Validators: []validator.String{
					stringvalidator.LengthAtMost(2048),
					stringvalidator.RegexMatches(executionIdentityRoleARN, "must be an IAM role ARN (arn:<partition>:iam::<account id>:role/<name>)"),
				},
			},
		},
	}
}

func executionIdentityDataSourceSchema() dsschema.SingleNestedAttribute {
	return dsschema.SingleNestedAttribute{
		Computed:            true,
		MarkdownDescription: "Principal the agent's runs act as.",
		Attributes: map[string]dsschema.Attribute{
			"type":     dsschema.StringAttribute{Computed: true, MarkdownDescription: "`service_account` (the Anthropic-managed default) or `aws_role`."},
			"role_arn": dsschema.StringAttribute{Computed: true, MarkdownDescription: "ARN of the IAM role (`aws_role` only)."},
		},
	}
}

// --- Config validation ---

// executionIdentityConfigValidator requires role_arn exactly for aws_role.
type executionIdentityConfigValidator struct{}

func (v *executionIdentityConfigValidator) Description(_ context.Context) string {
	return "Validates execution_identity: role_arn is required when type is aws_role and forbidden otherwise."
}

func (v *executionIdentityConfigValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v *executionIdentityConfigValidator) ValidateResource(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var obj types.Object
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("execution_identity"), &obj)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateExecutionIdentity(ctx, obj)...)
}

// validateExecutionIdentity treats unknown values as neither set nor missing.
func validateExecutionIdentity(ctx context.Context, obj types.Object) diag.Diagnostics {
	var diags diag.Diagnostics
	if obj.IsNull() || obj.IsUnknown() {
		return diags
	}
	var m agentExecutionIdentityModel
	diags.Append(obj.As(ctx, &m, basetypes.ObjectAsOptions{})...)
	if diags.HasError() || m.Type.IsNull() || m.Type.IsUnknown() {
		return diags
	}
	p := path.Root("execution_identity").AtName("role_arn")
	switch m.Type.ValueString() {
	case executionIdentityAWSRole:
		if m.RoleARN.IsNull() {
			diags.AddAttributeError(p, "Missing role ARN", `"role_arn" is required when "type" is "aws_role".`)
		}
	case executionIdentityServiceAccount:
		if !m.RoleARN.IsNull() && !m.RoleARN.IsUnknown() {
			diags.AddAttributeError(p, "Unexpected role ARN",
				`"role_arn" is only allowed when "type" is "aws_role": the service account carries no identifier.`)
		}
	}
	return diags
}

// --- Plan -> params ---

// buildExecutionIdentityParams returns the execution identity to send and
// whether to send it. A configured identity is always sent. A null plan is
// omitted on create, and on update while state is null too, so configurations
// that never set it keep their request body; a null plan over a non-null state
// (the attribute removed, or an identity set out of band that Read surfaced)
// sends the service account, which restores the default.
func buildExecutionIdentityParams(ctx context.Context, plan, state types.Object) (anthropic.BetaManagedAgentsExecutionIdentityParamsUnion, bool, diag.Diagnostics) {
	var diags diag.Diagnostics
	var u anthropic.BetaManagedAgentsExecutionIdentityParamsUnion

	if plan.IsNull() || plan.IsUnknown() {
		if state.IsNull() || state.IsUnknown() {
			return u, false, diags
		}
		sa := anthropic.NewBetaManagedAgentsServiceAccountExecutionIdentityParams()
		u.OfServiceAccount = &sa
		return u, true, diags
	}

	var m agentExecutionIdentityModel
	diags.Append(plan.As(ctx, &m, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return u, false, diags
	}
	switch m.Type.ValueString() {
	case executionIdentityServiceAccount:
		sa := anthropic.NewBetaManagedAgentsServiceAccountExecutionIdentityParams()
		u.OfServiceAccount = &sa
	case executionIdentityAWSRole:
		u = anthropic.BetaManagedAgentsExecutionIdentityParamsOfAWSRole(m.RoleARN.ValueString())
	default:
		diags.AddAttributeError(path.Root("execution_identity").AtName("type"), "Unsupported execution identity type",
			fmt.Sprintf("%q is not a supported execution identity type. Expected service_account or aws_role.", m.Type.ValueString()))
		return u, false, diags
	}
	return u, true, diags
}

// --- Response -> state ---

// executionIdentityObject maps the API identity verbatim, null when absent.
func executionIdentityObject(e anthropic.BetaManagedAgentsExecutionIdentityUnion) (types.Object, diag.Diagnostics) {
	if e.Type == "" {
		return types.ObjectNull(agentExecutionIdentityAttrTypes), nil
	}
	return types.ObjectValue(agentExecutionIdentityAttrTypes, map[string]attr.Value{
		"type":     types.StringValue(e.Type),
		"role_arn": tfvalue.StringOrNull(e.RoleARN),
	})
}

// mapExecutionIdentityToState maps the API identity for the resource. The API
// always returns one, `service_account` when none was set, so that default
// stays null while the configuration (current: plan or prior state) leaves the
// attribute unset; any other identity, an IAM role set out of band included,
// is mapped and surfaces as drift.
func mapExecutionIdentityToState(e anthropic.BetaManagedAgentsExecutionIdentityUnion, current types.Object) (types.Object, diag.Diagnostics) {
	if e.Type == executionIdentityServiceAccount && (current.IsNull() || current.IsUnknown()) {
		return types.ObjectNull(agentExecutionIdentityAttrTypes), nil
	}
	return executionIdentityObject(e)
}
