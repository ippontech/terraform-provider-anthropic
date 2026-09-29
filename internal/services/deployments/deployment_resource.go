// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package deployments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &DeploymentResource{}
var _ resource.ResourceWithImportState = &DeploymentResource{}

// The production bounds of awaitDeploymentUpdateVisible's wait. They are
// consts, and the function takes them as arguments, so the unit tests can
// shrink the loop to milliseconds without mutating shared state — see the
// read-after-write note on the vaults API (vault_resource.go), which shares
// the same underlying Beta managed-agents infrastructure and has not been
// ruled out for this endpoint.
const (
	deploymentConsistencyTimeout  = 5 * time.Second
	deploymentConsistencyInterval = 200 * time.Millisecond
)

var deploymentScheduleAttrTypes = map[string]attr.Type{
	"expression": types.StringType,
	"timezone":   types.StringType,
}

func NewDeploymentResource() resource.Resource {
	return &DeploymentResource{}
}

// DeploymentResource defines the resource implementation.
type DeploymentResource struct {
	client *anthropic.Client
}

// DeploymentResourceModel describes the resource data model. initial_events,
// resources, and budget are deep, multi-variant SDK unions (initial_events
// alone spans three event types, each with their own content-block
// sub-unions); modeling all of it as native Terraform nested attributes would
// dwarf the rest of the schema, so — per the jsontypes.Normalized convention
// already used for input_schema in agent tool configs — they are exposed as
// raw-JSON string attributes carrying exactly the API's request/response
// shape.
type DeploymentResourceModel struct {
	ID            types.String         `tfsdk:"id"`
	Name          types.String         `tfsdk:"name"`
	AgentID       types.String         `tfsdk:"agent_id"`
	EnvironmentID types.String         `tfsdk:"environment_id"`
	Description   types.String         `tfsdk:"description"`
	InitialEvents jsontypes.Normalized `tfsdk:"initial_events"`
	Metadata      types.Map            `tfsdk:"metadata"`
	Resources     jsontypes.Normalized `tfsdk:"resources"`
	Budget        jsontypes.Normalized `tfsdk:"budget"`
	VaultIDs      types.List           `tfsdk:"vault_ids"`
	Schedule      types.Object         `tfsdk:"schedule"`
	Paused        types.Bool           `tfsdk:"paused"`
	Status        types.String         `tfsdk:"status"`
	CreatedAt     types.String         `tfsdk:"created_at"`
	UpdatedAt     types.String         `tfsdk:"updated_at"`
	ArchivedAt    types.String         `tfsdk:"archived_at"`
}

// DeploymentScheduleModel is the nested model backing the "schedule" object
// attribute.
type DeploymentScheduleModel struct {
	Expression types.String `tfsdk:"expression"`
	Timezone   types.String `tfsdk:"timezone"`
}

// --- Schema ---

func (r *DeploymentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_deployment"
}

func (r *DeploymentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a scheduled deployment (beta): a configured instance of an agent that binds it to an environment, " +
			"credentials, initial events, and an optional cron schedule so it can run autonomously. There is no hard-delete endpoint for " +
			"deployments: destroying this resource always archives it.\n\n" +
			"`initial_events`, `resources`, and `budget` accept raw JSON matching the API's own request shape (see the API reference), " +
			"since Terraform-native modeling of their deeply nested unions would dwarf the rest of this schema.",
		Attributes: map[string]schema.Attribute{
			// --- Required ---
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Human-readable name for the deployment.",
			},
			"agent_id": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "ID of the `agent` to deploy (`agent_...`). Accepts only the plain agent ID, which pins the latest " +
					"version at create time and re-pins to the latest version on every update. The agent must exist and not be archived.",
			},
			"environment_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "ID of the `environment` defining the container configuration for sessions created from this deployment.",
			},
			"initial_events": schema.StringAttribute{
				Required:   true,
				CustomType: jsontypes.NormalizedType{},
				MarkdownDescription: "JSON array of events sent to each session immediately after creation. At least 1, maximum 50. Each " +
					"element is one of the API's `user.message`, `user.define_outcome`, or `system.message` event shapes, e.g. " +
					"`jsonencode([{type = \"user.message\", content = [{type = \"text\", text = \"...\"}]}])`.",
			},

			// --- Optional ---
			"description": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Description of what the deployment does.",
			},
			"metadata": schema.MapAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Arbitrary key-value metadata. Maximum 16 pairs, keys up to 64 characters, values up to 512 characters.",
			},
			"resources": schema.StringAttribute{
				Optional:   true,
				CustomType: jsontypes.NormalizedType{},
				MarkdownDescription: "JSON array of resources (e.g. repositories, files, memory stores) mounted into each session's " +
					"container. Maximum 500. Full replacement on update; cannot be cleared once set.",
			},
			"budget": schema.StringAttribute{
				Optional:   true,
				CustomType: jsontypes.NormalizedType{},
				MarkdownDescription: "JSON object for a hard spend ceiling: sessions stop issuing new model requests once the tracked " +
					"list cost reaches `max_list_cost`, e.g. " +
					"`jsonencode({max_list_cost = {amount = \"2500\", currency = \"USD\"}, type = \"limit\"})`.",
			},
			"vault_ids": schema.ListAttribute{
				Optional:    true,
				ElementType: types.StringType,
				MarkdownDescription: "Vault IDs supplying stored credentials for sessions created from this deployment. Maximum 50. Full " +
					"replacement on update; cannot be cleared once set.",
			},
			"schedule": schema.SingleNestedAttribute{
				Optional: true,
				MarkdownDescription: "5-field POSIX cron schedule. A deployment without a schedule only runs when triggered manually " +
					"(outside the scope of this resource). Cannot be cleared once set — the update API has no way to remove a schedule.",
				Attributes: map[string]schema.Attribute{
					"expression": schema.StringAttribute{
						Required: true,
						MarkdownDescription: "5-field POSIX cron expression: minute hour day-of-month month day-of-week (e.g. `0 9 * * 1-5` " +
							"for weekdays at 9am).",
					},
					"timezone": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "IANA timezone identifier (e.g. `America/Los_Angeles`, `UTC`).",
					},
				},
			},
			"paused": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "Whether the deployment is paused. Changing this calls the dedicated pause/unpause endpoints rather than the general update endpoint. Default: `false`.",
			},

			// --- Computed ---
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Unique deployment identifier assigned by the API (`depl_...`).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"status": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Lifecycle status of the deployment: `active` or `paused`.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the deployment was created.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the deployment was last updated.",
			},
			"archived_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the deployment was archived, or null while it is live.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

// --- Configure ---

func (r *DeploymentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	pd, ok := req.ProviderData.(*providerdata.ProviderData)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *providerdata.ProviderData, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}

	if !providerrors.RequireResourceAPIClient(pd.Client, &resp.Diagnostics) {
		return
	}

	r.client = pd.Client
}

// --- Create ---

func (r *DeploymentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data DeploymentResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	params, diags := buildDeploymentCreateParams(ctx, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	deployment, err := r.client.Beta.Deployments.New(ctx, params)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create deployment: %s", err))
		return
	}

	if data.Paused.ValueBool() {
		deployment, err = r.client.Beta.Deployments.Pause(ctx, deployment.ID, anthropic.BetaDeploymentPauseParams{})
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to pause newly created deployment: %s", err))
			return
		}
	}

	resp.Diagnostics.Append(mapDeploymentToState(ctx, deployment, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Read ---

func (r *DeploymentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data DeploymentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	deployment, err := r.client.Beta.Deployments.Get(ctx, data.ID.ValueString(), anthropic.BetaDeploymentGetParams{})
	if err != nil {
		var apierr *anthropic.Error
		if errors.As(err, &apierr) && apierr.StatusCode == 404 {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read deployment: %s", err))
		return
	}

	resp.Diagnostics.Append(mapDeploymentToState(ctx, deployment, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Update ---

// isTerminalDeploymentReadError reports whether a Get failure is one the poll
// can never recover from: the deployment is gone, or the credential no
// longer has access to it. Retrying those until the deadline would stall the
// apply for seconds on a read that will never converge.
func isTerminalDeploymentReadError(err error) bool {
	var apierr *anthropic.Error
	if !errors.As(err, &apierr) {
		return false
	}

	switch apierr.StatusCode {
	case 401, 403, 404:
		return true
	default:
		return false
	}
}

// awaitDeploymentUpdateVisible polls Get until the stored deployment is at
// least as new as writtenAt, the updated_at returned by the write itself.
// Mirrors awaitVaultUpdateVisible (vaults/vault_resource.go): the vaults API
// serves stale reads for up to ~1s after a write, and this endpoint shares
// the same underlying Beta managed-agents infrastructure but has not itself
// been probed for the same staleness (see CLAUDE.md's read-after-write
// section).
//
// It is deliberately best-effort: on a read error, a timeout, or a cancelled
// context it returns without reporting a diagnostic. The write has already
// succeeded, so failing the apply here would turn a cosmetic staleness window
// into a hard error; the worst case of giving up is the phantom diff we were
// trying to avoid.
func awaitDeploymentUpdateVisible(ctx context.Context, client *anthropic.Client, id string, writtenAt time.Time, timeout, interval time.Duration) {
	deadline := time.Now().Add(timeout)

	for {
		deployment, err := client.Beta.Deployments.Get(ctx, id, anthropic.BetaDeploymentGetParams{})
		switch {
		case err == nil:
			if !deployment.UpdatedAt.Before(writtenAt) {
				return
			}
		case isTerminalDeploymentReadError(err):
			tflog.Warn(ctx, "deployment became unreadable while waiting for the update to be visible; giving up on the consistency wait", map[string]any{
				"deployment_id": id,
				"error":         err.Error(),
			})
			return
		}

		if time.Now().After(deadline) {
			tflog.Warn(ctx, "deployment update not visible before the consistency timeout; the next plan may show a transient diff", map[string]any{
				"deployment_id": id,
				"timeout":       timeout.String(),
			})
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

func (r *DeploymentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan DeploymentResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state DeploymentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	params, diags := buildDeploymentUpdateParams(ctx, &plan, &state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	deployment, err := r.client.Beta.Deployments.Update(ctx, state.ID.ValueString(), params)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update deployment: %s", err))
		return
	}

	// The pause endpoints are separate from the general update endpoint;
	// reconcile "paused" only when it actually changed.
	if plan.Paused.ValueBool() != state.Paused.ValueBool() {
		if plan.Paused.ValueBool() {
			deployment, err = r.client.Beta.Deployments.Pause(ctx, state.ID.ValueString(), anthropic.BetaDeploymentPauseParams{})
		} else {
			deployment, err = r.client.Beta.Deployments.Unpause(ctx, state.ID.ValueString(), anthropic.BetaDeploymentUnpauseParams{})
		}
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to change deployment pause state: %s", err))
			return
		}
	}

	resp.Diagnostics.Append(mapDeploymentToState(ctx, deployment, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	awaitDeploymentUpdateVisible(ctx, r.client, state.ID.ValueString(), deployment.UpdatedAt, deploymentConsistencyTimeout, deploymentConsistencyInterval)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// --- Delete (always archives; there is no hard-delete endpoint) ---

func (r *DeploymentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data DeploymentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.Beta.Deployments.Archive(ctx, data.ID.ValueString(), anthropic.BetaDeploymentArchiveParams{})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to archive deployment: %s", err))
	}
}

// --- ImportState ---

func (r *DeploymentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// ============================================================================
// Helper functions
// ============================================================================

// buildDeploymentCreateParams converts the planned config into a create
// request.
func buildDeploymentCreateParams(ctx context.Context, data *DeploymentResourceModel) (anthropic.BetaDeploymentNewParams, diag.Diagnostics) {
	var diags diag.Diagnostics

	events, err := unmarshalInitialEvents(data.InitialEvents)
	if err != nil {
		diags.AddError("Invalid initial_events", err.Error())
		return anthropic.BetaDeploymentNewParams{}, diags
	}

	params := anthropic.BetaDeploymentNewParams{
		Agent:         anthropic.BetaDeploymentNewParamsAgentUnion{OfString: param.NewOpt(data.AgentID.ValueString())},
		EnvironmentID: data.EnvironmentID.ValueString(),
		InitialEvents: events,
		Name:          data.Name.ValueString(),
	}

	if !data.Description.IsNull() && !data.Description.IsUnknown() {
		params.Description = param.NewOpt(data.Description.ValueString())
	}

	if !data.Metadata.IsNull() && !data.Metadata.IsUnknown() {
		var md map[string]string
		diags.Append(data.Metadata.ElementsAs(ctx, &md, false)...)
		if diags.HasError() {
			return anthropic.BetaDeploymentNewParams{}, diags
		}
		params.Metadata = md
	}

	if !data.Resources.IsNull() && !data.Resources.IsUnknown() {
		resources, err := unmarshalResourcesNew(data.Resources)
		if err != nil {
			diags.AddError("Invalid resources", err.Error())
			return anthropic.BetaDeploymentNewParams{}, diags
		}
		params.Resources = resources
	}

	if !data.Budget.IsNull() && !data.Budget.IsUnknown() {
		budget, err := unmarshalBudget(data.Budget)
		if err != nil {
			diags.AddError("Invalid budget", err.Error())
			return anthropic.BetaDeploymentNewParams{}, diags
		}
		params.Budget = budget
	}

	if !data.VaultIDs.IsNull() && !data.VaultIDs.IsUnknown() {
		var vaultIDs []string
		diags.Append(data.VaultIDs.ElementsAs(ctx, &vaultIDs, false)...)
		if diags.HasError() {
			return anthropic.BetaDeploymentNewParams{}, diags
		}
		params.VaultIDs = vaultIDs
	}

	if !data.Schedule.IsNull() && !data.Schedule.IsUnknown() {
		sched, d := buildScheduleParams(ctx, data.Schedule)
		diags.Append(d...)
		if diags.HasError() {
			return anthropic.BetaDeploymentNewParams{}, diags
		}
		params.Schedule = sched
	}

	return params, diags
}

// buildDeploymentUpdateParams converts the planned config into an update
// request. metadata uses PATCH semantics (upsert planned keys, null out keys
// removed from state) since the update API preserves omitted keys and only
// deletes a key set to null; the typed map[string]string field can't carry
// per-key nulls, so the patch is sent via SetExtraFields like vaults'
// buildMetadataPatch. resources, vault_ids and initial_events are full
// replacement on the wire: they are resent whenever set in the plan, and left
// omitted (preserved server-side) when null — the API gives no way to send an
// explicit empty array through the typed `omitzero` params, so a config that
// removes an already-set value here cannot be cleared, matching the
// resources/vault_ids/schedule "cannot be cleared" notes in the schema.
func buildDeploymentUpdateParams(ctx context.Context, plan, state *DeploymentResourceModel) (anthropic.BetaDeploymentUpdateParams, diag.Diagnostics) {
	var diags diag.Diagnostics
	params := anthropic.BetaDeploymentUpdateParams{}

	if plan.Description.IsNull() {
		params.Description = param.Null[string]()
	} else if !plan.Description.IsUnknown() {
		params.Description = param.NewOpt(plan.Description.ValueString())
	}

	if !plan.EnvironmentID.IsNull() && !plan.EnvironmentID.IsUnknown() {
		params.EnvironmentID = param.NewOpt(plan.EnvironmentID.ValueString())
	}

	if !plan.Name.IsNull() && !plan.Name.IsUnknown() {
		params.Name = param.NewOpt(plan.Name.ValueString())
	}

	if !plan.AgentID.IsNull() && !plan.AgentID.IsUnknown() {
		params.Agent = anthropic.BetaDeploymentUpdateParamsAgentUnion{OfString: param.NewOpt(plan.AgentID.ValueString())}
	}

	metaPatch, d := buildMetadataPatch(ctx, plan.Metadata, state.Metadata)
	diags.Append(d...)
	if diags.HasError() {
		return anthropic.BetaDeploymentUpdateParams{}, diags
	}
	if len(metaPatch) > 0 {
		params.SetExtraFields(map[string]any{"metadata": metaPatch})
	}

	if !plan.Resources.IsNull() && !plan.Resources.IsUnknown() {
		resources, err := unmarshalResourcesUpdate(plan.Resources)
		if err != nil {
			diags.AddError("Invalid resources", err.Error())
			return anthropic.BetaDeploymentUpdateParams{}, diags
		}
		params.Resources = resources
	}

	if !plan.Budget.IsNull() && !plan.Budget.IsUnknown() {
		budget, err := unmarshalBudget(plan.Budget)
		if err != nil {
			diags.AddError("Invalid budget", err.Error())
			return anthropic.BetaDeploymentUpdateParams{}, diags
		}
		params.Budget = budget
	}

	if !plan.InitialEvents.IsNull() && !plan.InitialEvents.IsUnknown() {
		events, err := unmarshalInitialEvents(plan.InitialEvents)
		if err != nil {
			diags.AddError("Invalid initial_events", err.Error())
			return anthropic.BetaDeploymentUpdateParams{}, diags
		}
		params.InitialEvents = events
	}

	if !plan.VaultIDs.IsNull() && !plan.VaultIDs.IsUnknown() {
		var vaultIDs []string
		diags.Append(plan.VaultIDs.ElementsAs(ctx, &vaultIDs, false)...)
		if diags.HasError() {
			return anthropic.BetaDeploymentUpdateParams{}, diags
		}
		params.VaultIDs = vaultIDs
	}

	if !plan.Schedule.IsNull() && !plan.Schedule.IsUnknown() {
		sched, d := buildScheduleParams(ctx, plan.Schedule)
		diags.Append(d...)
		if diags.HasError() {
			return anthropic.BetaDeploymentUpdateParams{}, diags
		}
		params.Schedule = sched
	}

	return params, diags
}

// buildScheduleParams converts the nested "schedule" object attribute into
// the SDK's cron schedule params. The `type` field is always "cron" and is
// not exposed in the Terraform schema.
func buildScheduleParams(ctx context.Context, obj types.Object) (anthropic.BetaManagedAgentsScheduleParams, diag.Diagnostics) {
	var model DeploymentScheduleModel
	diags := obj.As(ctx, &model, basetypes.ObjectAsOptions{})
	if diags.HasError() {
		return anthropic.BetaManagedAgentsScheduleParams{}, diags
	}

	return anthropic.BetaManagedAgentsScheduleParams{
		Expression: model.Expression.ValueString(),
		Timezone:   model.Timezone.ValueString(),
		Type:       anthropic.BetaManagedAgentsScheduleParamsTypeCron,
	}, diags
}

// unmarshalInitialEvents decodes the initial_events JSON attribute into the
// SDK's typed union slice. The union types implement UnmarshalJSON via a
// registered discriminator on `type`, so json.Unmarshal alone resolves each
// element to the correct variant.
func unmarshalInitialEvents(v jsontypes.Normalized) ([]anthropic.BetaManagedAgentsDeploymentInitialEventParamsUnion, error) {
	var events []anthropic.BetaManagedAgentsDeploymentInitialEventParamsUnion
	if err := json.Unmarshal([]byte(v.ValueString()), &events); err != nil {
		return nil, err
	}
	return events, nil
}

// unmarshalResourcesNew decodes the resources JSON attribute for a create
// request.
func unmarshalResourcesNew(v jsontypes.Normalized) ([]anthropic.BetaDeploymentNewParamsResourceUnion, error) {
	var resources []anthropic.BetaDeploymentNewParamsResourceUnion
	if err := json.Unmarshal([]byte(v.ValueString()), &resources); err != nil {
		return nil, err
	}
	return resources, nil
}

// unmarshalResourcesUpdate decodes the resources JSON attribute for an
// update request. A separate (but structurally identical) union type from
// unmarshalResourcesNew, mirroring the SDK's New/Update param split.
func unmarshalResourcesUpdate(v jsontypes.Normalized) ([]anthropic.BetaDeploymentUpdateParamsResourceUnion, error) {
	var resources []anthropic.BetaDeploymentUpdateParamsResourceUnion
	if err := json.Unmarshal([]byte(v.ValueString()), &resources); err != nil {
		return nil, err
	}
	return resources, nil
}

// unmarshalBudget decodes the budget JSON attribute into the SDK's budget
// limit params.
func unmarshalBudget(v jsontypes.Normalized) (anthropic.BetaManagedAgentsBudgetLimitParam, error) {
	var budget anthropic.BetaManagedAgentsBudgetLimitParam
	if err := json.Unmarshal([]byte(v.ValueString()), &budget); err != nil {
		return anthropic.BetaManagedAgentsBudgetLimitParam{}, err
	}
	return budget, nil
}

// buildMetadataPatch builds a PATCH-semantics metadata map (a string value
// upserts a key, nil deletes it) suitable for SetExtraFields. Mirrors
// vaults.buildMetadataPatch (vault_resource.go) — see CLAUDE.md's "Map
// attributes with PATCH semantics" section.
func buildMetadataPatch(ctx context.Context, plan, state types.Map) (map[string]any, diag.Diagnostics) {
	var diags diag.Diagnostics
	patch := map[string]any{}

	if !plan.IsNull() && !plan.IsUnknown() {
		var pm map[string]string
		diags.Append(plan.ElementsAs(ctx, &pm, false)...)
		if diags.HasError() {
			return nil, diags
		}
		for k, v := range pm {
			patch[k] = v
		}
	}

	if !state.IsNull() && !state.IsUnknown() {
		var sm map[string]string
		diags.Append(state.ElementsAs(ctx, &sm, false)...)
		if diags.HasError() {
			return nil, diags
		}
		for k := range sm {
			if _, ok := patch[k]; !ok {
				patch[k] = nil
			}
		}
	}

	return patch, diags
}

// mapDeploymentToState maps the API response to the Terraform state model.
func mapDeploymentToState(ctx context.Context, deployment *anthropic.BetaManagedAgentsDeployment, data *DeploymentResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics

	data.ID = types.StringValue(deployment.ID)
	data.Name = types.StringValue(deployment.Name)
	data.AgentID = types.StringValue(deployment.Agent.ID)
	data.EnvironmentID = types.StringValue(deployment.EnvironmentID)
	data.Status = types.StringValue(string(deployment.Status))
	data.Paused = types.BoolValue(deployment.Status == anthropic.BetaManagedAgentsDeploymentStatusPaused)
	data.CreatedAt = types.StringValue(deployment.CreatedAt.Format(time.RFC3339))
	data.UpdatedAt = types.StringValue(deployment.UpdatedAt.Format(time.RFC3339))
	data.ArchivedAt = tfvalue.TimeOrNull(deployment.ArchivedAt)

	data.Description = descriptionOrNull(deployment.Description, data.Description)

	if events := deployment.InitialEvents; len(events) > 0 {
		raw := make([]json.RawMessage, len(events))
		for i, e := range events {
			raw[i] = json.RawMessage(e.RawJSON())
		}
		b, err := json.Marshal(raw)
		if err != nil {
			diags.AddError("Unable to encode initial_events", err.Error())
			return diags
		}
		data.InitialEvents = jsontypes.NewNormalizedValue(string(b))
	} else {
		data.InitialEvents = jsontypes.NewNormalizedNull()
	}

	if len(deployment.Metadata) > 0 {
		mv, d := types.MapValueFrom(ctx, types.StringType, deployment.Metadata)
		diags.Append(d...)
		data.Metadata = mv
	} else {
		data.Metadata = types.MapNull(types.StringType)
	}

	if resources := deployment.Resources; len(resources) > 0 {
		raw := make([]json.RawMessage, len(resources))
		for i, res := range resources {
			raw[i] = json.RawMessage(res.RawJSON())
		}
		b, err := json.Marshal(raw)
		if err != nil {
			diags.AddError("Unable to encode resources", err.Error())
			return diags
		}
		data.Resources = jsontypes.NewNormalizedValue(string(b))
	} else {
		data.Resources = jsontypes.NewNormalizedNull()
	}

	if budgetRaw := deployment.Budget.RawJSON(); budgetRaw != "" && budgetRaw != "null" {
		data.Budget = jsontypes.NewNormalizedValue(budgetRaw)
	} else {
		data.Budget = jsontypes.NewNormalizedNull()
	}

	if len(deployment.VaultIDs) > 0 {
		lv, d := types.ListValueFrom(ctx, types.StringType, deployment.VaultIDs)
		diags.Append(d...)
		data.VaultIDs = lv
	} else {
		data.VaultIDs = types.ListNull(types.StringType)
	}

	if deployment.Schedule.Expression != "" {
		sv, d := types.ObjectValue(deploymentScheduleAttrTypes, map[string]attr.Value{
			"expression": types.StringValue(deployment.Schedule.Expression),
			"timezone":   types.StringValue(deployment.Schedule.Timezone),
		})
		diags.Append(d...)
		data.Schedule = sv
	} else {
		data.Schedule = types.ObjectNull(deploymentScheduleAttrTypes)
	}

	return diags
}

// descriptionOrNull maps the API's description the same way
// serviceaccounts.descriptionOrNull does, with one exception: description is
// Optional (not Computed), so Terraform requires the final state to echo
// back a practitioner-configured empty string rather than collapse it to
// null. planned is data.Description as already populated from Plan
// (Create/Update) or State (Read) before this call; a known non-null value
// there means the practitioner explicitly configured `description = ""`,
// which must round-trip as "" instead of null.
func descriptionOrNull(apiValue string, planned types.String) types.String {
	if apiValue == "" {
		if !planned.IsNull() && !planned.IsUnknown() {
			return types.StringValue("")
		}
		return types.StringNull()
	}
	return types.StringValue(apiValue)
}
