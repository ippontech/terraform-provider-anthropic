// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package deployments

import (
	"context"
	"fmt"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

var _ datasource.DataSource = &DeploymentRunsDataSource{}

func NewDeploymentRunsDataSource() datasource.DataSource {
	return &DeploymentRunsDataSource{}
}

type DeploymentRunsDataSource struct {
	client *anthropic.Client
}

type DeploymentRunsDataSourceModel struct {
	DeploymentID types.String `tfsdk:"deployment_id"`
	HasError     types.Bool   `tfsdk:"has_error"`
	TriggerType  types.String `tfsdk:"trigger_type"`
	Runs         types.List   `tfsdk:"runs"`
}

var deploymentRunAgentAttrTypes = map[string]attr.Type{
	"id":      types.StringType,
	"type":    types.StringType,
	"version": types.Int64Type,
}

var deploymentRunErrorAttrTypes = map[string]attr.Type{
	"type":    types.StringType,
	"message": types.StringType,
}

var deploymentRunTriggerContextAttrTypes = map[string]attr.Type{
	"type":         types.StringType,
	"scheduled_at": types.StringType,
}

var deploymentRunItemAttrTypes = map[string]attr.Type{
	"id":              types.StringType,
	"deployment_id":   types.StringType,
	"session_id":      types.StringType,
	"error":           types.ObjectType{AttrTypes: deploymentRunErrorAttrTypes},
	"trigger_context": types.ObjectType{AttrTypes: deploymentRunTriggerContextAttrTypes},
	"agent":           types.ObjectType{AttrTypes: deploymentRunAgentAttrTypes},
	"created_at":      types.StringType,
	"type":            types.StringType,
}

func (d *DeploymentRunsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_deployment_runs"
}

func (d *DeploymentRunsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the run history of scheduled deployments (beta). Each run records one attempt at starting a " +
			"session: either the resulting `session_id` or the `error` that prevented session creation. All pages are fetched " +
			"automatically. Omit `deployment_id` to list runs across every deployment in the workspace.",
		Attributes: map[string]schema.Attribute{
			"deployment_id": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Restrict results to the runs of this deployment (`depl_...`). A well-formed but unknown ID yields an empty list; a malformed ID is rejected by the API with `Invalid deployment ID`.",
			},
			"has_error": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "When `true`, only failed runs (non-null `error`) are returned; when `false`, only successful runs (non-null `session_id`). Omit for both.",
			},
			"trigger_type": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Restrict results to runs with this trigger. One of `schedule` or `manual`.",
				Validators: []validator.String{
					stringvalidator.OneOf("schedule", "manual"),
				},
			},
			"runs": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "List of deployment runs matching the given filters.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Unique run identifier (`drun_...`).",
						},
						"deployment_id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "ID of the deployment that produced this run.",
						},
						"session_id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "ID of the session started by this run. Null when session creation failed.",
						},
						"error": schema.SingleNestedAttribute{
							Computed:            true,
							MarkdownDescription: "Why the run failed to create a session. Null on success.",
							Attributes: map[string]schema.Attribute{
								"type": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "Failure type (e.g. `environment_archived_error`, `agent_archived_error`, `session_rate_limited_error`).",
								},
								"message": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "Human-readable error description.",
								},
							},
						},
						"trigger_context": schema.SingleNestedAttribute{
							Computed:            true,
							MarkdownDescription: "What triggered the run.",
							Attributes: map[string]schema.Attribute{
								"type": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "Trigger type. One of `schedule` or `manual`.",
								},
								"scheduled_at": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "Scheduled fire time (RFC 3339) for `schedule` triggers. Null for manual runs.",
								},
							},
						},
						"agent": schema.SingleNestedAttribute{
							Computed:            true,
							MarkdownDescription: "The agent version the run resolved to.",
							Attributes: map[string]schema.Attribute{
								"id": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "Agent identifier (`agent_...`).",
								},
								"type": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "Reference type. Always `agent`.",
								},
								"version": schema.Int64Attribute{
									Computed:            true,
									MarkdownDescription: "Concrete agent version the run used.",
								},
							},
						},
						"created_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Creation timestamp (RFC 3339).",
						},
						"type": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Object type. Always `deployment_run`.",
						},
					},
				},
			},
		},
	}
}

func (d *DeploymentRunsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	pd, ok := req.ProviderData.(*providerdata.ProviderData)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *providerdata.ProviderData, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}

	if !providerrors.RequireDataSourceAPIClient(pd.Client, &resp.Diagnostics) {
		return
	}

	d.client = pd.Client
}

func (d *DeploymentRunsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data DeploymentRunsDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	pager := d.client.Beta.DeploymentRuns.ListAutoPaging(ctx, buildDeploymentRunListParams(data))

	runObjs := make([]attr.Value, 0)
	for pager.Next() {
		obj, diags := mapDeploymentRunToObject(pager.Current())
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		runObjs = append(runObjs, obj)
	}

	if err := pager.Err(); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list deployment runs: %s", err))
		return
	}

	runsList, diags := types.ListValue(types.ObjectType{AttrTypes: deploymentRunItemAttrTypes}, runObjs)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	data.Runs = runsList
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// buildDeploymentRunListParams forwards only the filters that are set in
// config; a null or unknown value is genuinely omitted from the query.
func buildDeploymentRunListParams(data DeploymentRunsDataSourceModel) anthropic.BetaDeploymentRunListParams {
	params := anthropic.BetaDeploymentRunListParams{}
	if !data.DeploymentID.IsNull() && !data.DeploymentID.IsUnknown() {
		params.DeploymentID = param.NewOpt(data.DeploymentID.ValueString())
	}
	if !data.HasError.IsNull() && !data.HasError.IsUnknown() {
		params.HasError = param.NewOpt(data.HasError.ValueBool())
	}
	if !data.TriggerType.IsNull() && !data.TriggerType.IsUnknown() {
		params.TriggerType = anthropic.BetaManagedAgentsTriggerType(data.TriggerType.ValueString())
	}
	return params
}

// mapDeploymentRunToObject converts one API run into a Terraform object value
// matching deploymentRunItemAttrTypes. Exactly one of session_id and error is
// non-null on the wire; the same exclusivity is preserved in state.
func mapDeploymentRunToObject(run anthropic.BetaManagedAgentsDeploymentRun) (attr.Value, diag.Diagnostics) {
	var diags diag.Diagnostics

	sessionID := types.StringNull()
	if run.SessionID != "" {
		sessionID = types.StringValue(run.SessionID)
	}

	errorObj := types.ObjectNull(deploymentRunErrorAttrTypes)
	if run.Error.Type != "" {
		obj, d := types.ObjectValue(deploymentRunErrorAttrTypes, map[string]attr.Value{
			"type":    types.StringValue(run.Error.Type),
			"message": types.StringValue(run.Error.Message),
		})
		diags.Append(d...)
		errorObj = obj
	}

	scheduledAt := types.StringNull()
	if !run.TriggerContext.ScheduledAt.IsZero() {
		scheduledAt = types.StringValue(run.TriggerContext.ScheduledAt.Format(time.RFC3339))
	}
	triggerObj, d := types.ObjectValue(deploymentRunTriggerContextAttrTypes, map[string]attr.Value{
		"type":         types.StringValue(run.TriggerContext.Type),
		"scheduled_at": scheduledAt,
	})
	diags.Append(d...)

	agentObj, d := types.ObjectValue(deploymentRunAgentAttrTypes, map[string]attr.Value{
		"id":      types.StringValue(run.Agent.ID),
		"type":    types.StringValue(string(run.Agent.Type)),
		"version": types.Int64Value(run.Agent.Version),
	})
	diags.Append(d...)

	obj, d := types.ObjectValue(deploymentRunItemAttrTypes, map[string]attr.Value{
		"id":              types.StringValue(run.ID),
		"deployment_id":   types.StringValue(run.DeploymentID),
		"session_id":      sessionID,
		"error":           errorObj,
		"trigger_context": triggerObj,
		"agent":           agentObj,
		"created_at":      types.StringValue(run.CreatedAt.Format(time.RFC3339)),
		"type":            types.StringValue(string(run.Type)),
	})
	diags.Append(d...)

	return obj, diags
}
