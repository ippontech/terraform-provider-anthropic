// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package serviceaccounts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &ServiceAccountWorkspaceResource{}
var _ resource.ResourceWithImportState = &ServiceAccountWorkspaceResource{}

// The production bounds of awaitServiceAccountWorkspaceRoleVisible's wait.
// Consts passed as arguments, not package-level vars, so unit tests can shrink
// the loop to milliseconds without racing the tests that run the real Update
// in the same binary. Same bounds as the other WIF waits.
const (
	serviceAccountWorkspaceConsistencyTimeout  = 5 * time.Second
	serviceAccountWorkspaceConsistencyInterval = 200 * time.Millisecond
)

func NewServiceAccountWorkspaceResource() resource.Resource {
	return &ServiceAccountWorkspaceResource{}
}

// ServiceAccountWorkspaceResource defines the resource implementation. It
// uses the OAuth bearer client: these endpoints reject API keys outright.
type ServiceAccountWorkspaceResource struct {
	client *providerdata.OAuthClient
}

// --- Terraform data models ---

// ServiceAccountWorkspaceResourceModel describes the resource data model.
type ServiceAccountWorkspaceResourceModel struct {
	ID               types.String `tfsdk:"id"`
	ServiceAccountID types.String `tfsdk:"service_account_id"`
	WorkspaceID      types.String `tfsdk:"workspace_id"`
	WorkspaceRole    types.String `tfsdk:"workspace_role"`
	Implicit         types.Bool   `tfsdk:"implicit"`
	CreatedByActorID types.String `tfsdk:"created_by_actor_id"`
}

// --- Schema ---

func (r *ServiceAccountWorkspaceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_service_account_workspace"
}

func (r *ServiceAccountWorkspaceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Assigns a Workload Identity Federation service account to a workspace with a given role, creating an explicit membership. " +
			"Every service account already has an implicit membership in the organization's default workspace; this resource manages explicit memberships only, " +
			"which are required before federated tokens minted for that service account can act in a non-default workspace.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Composite identifier in the form `<service_account_id>:<workspace_id>`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"service_account_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Tagged ID of the service account to assign to the workspace. Immutable: changing it forces replacement.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"workspace_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Tagged ID of the workspace to assign the service account to. Immutable: changing it forces replacement.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"workspace_role": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Role to assign to the service account in the workspace. Valid values: `workspace_admin`, `workspace_developer`, " +
					"`workspace_restricted_developer`, `workspace_user` (service accounts cannot hold `workspace_billing`, so the API type already excludes it). " +
					"Changing it updates the membership in place: the new role replaces the current one, and the membership is never removed in between.",
				Validators: []validator.String{
					stringvalidator.OneOf(
						"workspace_admin",
						"workspace_developer",
						"workspace_restricted_developer",
						"workspace_user",
					),
				},
			},
			"implicit": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "True when this is the implicit default-workspace membership every service account has when no explicit membership exists. Always `false` for a membership managed by this resource.",
				PlanModifiers:       []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"created_by_actor_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Tagged ID (`user_`/`svac_`) of the actor that created this membership.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

// --- Configure ---

func (r *ServiceAccountWorkspaceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

	if !providerrors.RequireOAuthResourceClient(pd.OAuthClient, &resp.Diagnostics) {
		return
	}

	r.client = pd.OAuthClient
}

// --- Create ---

func (r *ServiceAccountWorkspaceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data ServiceAccountWorkspaceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	member, err := addServiceAccountToWorkspace(ctx, r.client, data.ServiceAccountID.ValueString(), data.WorkspaceID.ValueString(), data.WorkspaceRole.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to add service account to workspace: %s", err))
		return
	}

	mapServiceAccountWorkspaceToState(member, &data)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Read ---

// Read has no single-object GET endpoint: it lists every workspace the
// service account belongs to and looks for the one this resource tracks. If
// the membership is gone from that list (removed out-of-band, the workspace
// was deleted, or the service account was archived), the resource is dropped
// from state so the next plan recreates it instead of erroring forever.
func (r *ServiceAccountWorkspaceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data ServiceAccountWorkspaceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	member, err := findServiceAccountWorkspaceMembership(ctx, r.client, data.ServiceAccountID.ValueString(), data.WorkspaceID.ValueString())
	if err != nil {
		var apierr *anthropic.Error
		if errors.As(err, &apierr) && apierr.StatusCode == 404 {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list service account workspace memberships: %s", err))
		return
	}
	if member == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	mapServiceAccountWorkspaceToState(member, &data)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Update ---

// Update changes workspace_role in place, the only attribute that reaches it
// (the two IDs are RequiresReplace, the rest is Computed). The API's update
// endpoint lives under the workspace, not under the service account like Add,
// List and Remove: POST
// /v1/organizations/workspaces/{workspace_id}/service_accounts/{service_account_id}.
// It only accepts explicit memberships, which is all this resource tracks.
//
// A 404 means the membership is gone since the plan (an apply with
// -refresh=false, or a removal between plan and apply): it is added back with
// the planned role, as the replace this attribute used to force did.
//
// The state is the plan: the write leaves the IDs alone, and implicit and
// created_by_actor_id are pinned from state by UseStateForUnknown, so taking
// them from a response nobody probed live could only turn a successful role
// change into an inconsistent-result error. The next refresh reads them back.
func (r *ServiceAccountWorkspaceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan ServiceAccountWorkspaceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state ServiceAccountWorkspaceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serviceAccountID := state.ServiceAccountID.ValueString()
	workspaceID := state.WorkspaceID.ValueString()
	role := plan.WorkspaceRole.ValueString()

	_, err := updateServiceAccountWorkspaceRole(ctx, r.client, serviceAccountID, workspaceID, role)
	var apierr *anthropic.Error
	if errors.As(err, &apierr) && apierr.StatusCode == 404 {
		tflog.Warn(ctx, "service account workspace membership not found on role update; adding it back with the planned role",
			map[string]any{"service_account_id": serviceAccountID, "workspace_id": workspaceID})
		if _, addErr := addServiceAccountToWorkspace(ctx, r.client, serviceAccountID, workspaceID, role); addErr != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to add service account %s back to workspace %s after the role update found no membership (%s): %s", serviceAccountID, workspaceID, err, addErr))
			return
		}
		err = nil
	}
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update the role of service account %s in workspace %s: %s", serviceAccountID, workspaceID, err))
		return
	}

	awaitServiceAccountWorkspaceRoleVisible(ctx, r.client, serviceAccountID, workspaceID, role, serviceAccountWorkspaceConsistencyTimeout, serviceAccountWorkspaceConsistencyInterval)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// --- Delete ---

// Delete is a hard remove: there is no archive concept for this membership.
// The API documents removal as idempotent (200 even if already removed), so
// no special 404 handling is needed here.
func (r *ServiceAccountWorkspaceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data ServiceAccountWorkspaceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// NOTE: the SDK signature is inverted relative to every other resource in
	// this provider. The positional argument is the workspace ID, and the
	// service account ID travels inside the params struct as a path
	// parameter. Swapping these silently targets the wrong URL
	// (v1/organizations/service_accounts/{service_account_id}/workspaces/{workspace_id})
	// since both are plausible-looking tagged IDs; removeServiceAccountFromWorkspace
	// isolates the call so a unit test can assert the request path directly.
	err := removeServiceAccountFromWorkspace(ctx, r.client, data.ServiceAccountID.ValueString(), data.WorkspaceID.ValueString())
	if err != nil {
		if providerrors.IsNotFound(err) {
			return // already gone
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to remove service account from workspace: %s", err))
		return
	}
}

// --- ImportState ---

func (r *ServiceAccountWorkspaceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.SplitN(req.ID, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			"Expected format: <service_account_id>:<workspace_id>",
		)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("service_account_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("workspace_id"), parts[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

// ============================================================================
// Helper functions
// ============================================================================

// addServiceAccountToWorkspace wraps the Add call so Create's param
// construction can be unit tested directly against an httptest server,
// without needing to fabricate a full resource.CreateRequest.
func addServiceAccountToWorkspace(ctx context.Context, client *providerdata.OAuthClient, serviceAccountID, workspaceID, workspaceRole string) (*anthropic.BetaServiceAccountWorkspaceMember, error) {
	params := anthropic.BetaOrganizationServiceAccountWorkspaceAddParams{
		WorkspaceID:   workspaceID,
		WorkspaceRole: anthropic.BetaNoBillingWorkspaceRole(workspaceRole),
	}
	return client.Beta.Organization.ServiceAccounts.Workspaces.Add(ctx, serviceAccountID, params)
}

// updateServiceAccountWorkspaceRole wraps the workspace-side Update call. Like
// removeServiceAccountFromWorkspace, it takes the natural (serviceAccountID,
// workspaceID) order and keeps the SDK's own order to this one call site:
// Workspaces.ServiceAccounts.Update takes the service account ID positionally
// and the workspace ID inside params, the reverse of
// ServiceAccounts.Workspaces.Remove. Both IDs are plausible tagged strings,
// so a swap compiles and targets the wrong URL; a unit test pins the path.
func updateServiceAccountWorkspaceRole(ctx context.Context, client *providerdata.OAuthClient, serviceAccountID, workspaceID, workspaceRole string) (*anthropic.BetaServiceAccountWorkspaceMember, error) {
	return client.Beta.Organization.Workspaces.ServiceAccounts.Update(ctx, serviceAccountID, anthropic.BetaOrganizationWorkspaceServiceAccountUpdateParams{
		WorkspaceID:   workspaceID,
		WorkspaceRole: anthropic.BetaNoBillingWorkspaceRole(workspaceRole),
	})
}

// awaitServiceAccountWorkspaceRoleVisible polls the membership list Read
// uses until the tracked membership carries role, the value just written.
// The write goes through the workspace-side endpoint and Read lists through
// the service-account side; neither was probed for read-after-write
// staleness, but the sibling WIF endpoints served a stale read after 1 in 9
// writes on 2026-09-08 (CLAUDE.md, "WIF endpoints"), and a stale role here
// would make the next plan propose the change again. Same shape as
// awaitServiceAccountUpdateVisible, with a role comparison instead of an
// updated_at one since memberships carry no timestamp: best-effort (never a
// diagnostic, the write already succeeded), bounded by consts passed as
// arguments, and stopped by a terminal read error or a membership no longer
// listed (a stale list still holds a membership that existed before the
// write, so its absence means it was removed, which Read reports as drift).
// The polls run under a context bounded by timeout, so a slow or throttled
// list (the SDK retries and honors retry-after) is cut off at the deadline
// instead of running past it.
func awaitServiceAccountWorkspaceRoleVisible(ctx context.Context, client *providerdata.OAuthClient, serviceAccountID, workspaceID, role string, timeout, interval time.Duration) {
	deadline := time.Now().Add(timeout)
	ctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()

	for {
		member, err := findServiceAccountWorkspaceMembership(ctx, client, serviceAccountID, workspaceID)
		switch {
		case err == nil && member == nil:
			tflog.Warn(ctx, "service account workspace membership no longer listed while waiting for the role update to be visible; giving up on the consistency wait",
				map[string]any{"service_account_id": serviceAccountID, "workspace_id": workspaceID})
			return
		case err == nil:
			if string(member.WorkspaceRole) == role {
				return
			}
		case isTerminalServiceAccountReadError(err):
			tflog.Warn(ctx, "service account workspace memberships became unreadable while waiting for the role update to be visible; giving up on the consistency wait",
				map[string]any{"service_account_id": serviceAccountID, "workspace_id": workspaceID, "error": err.Error()})
			return
		}

		if time.Now().After(deadline) {
			tflog.Warn(ctx, "service account workspace role update not visible before the consistency timeout; the next plan may show a transient diff",
				map[string]any{"service_account_id": serviceAccountID, "workspace_id": workspaceID, "timeout": timeout.String()})
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

// removeServiceAccountFromWorkspace wraps the Remove call. Its signature is
// intentionally the natural (serviceAccountID, workspaceID) order expected by
// every caller in this file, isolating the SDK's inverted argument order
// (workspaceID positional, service_account_id inside params) to this one
// call site so it can be locked in with a request-path assertion.
func removeServiceAccountFromWorkspace(ctx context.Context, client *providerdata.OAuthClient, serviceAccountID, workspaceID string) error {
	_, err := client.Beta.Organization.ServiceAccounts.Workspaces.Remove(ctx, workspaceID, anthropic.BetaOrganizationServiceAccountWorkspaceRemoveParams{
		ServiceAccountID: serviceAccountID,
	})
	return err
}

// findServiceAccountWorkspaceMembership pages through every workspace the
// service account belongs to and returns the explicit (non-implicit)
// membership matching workspaceID, or nil if none is found. A nil result
// with a nil error means "gone from the list": the caller should drop the
// resource from state.
//
// Implicit memberships (the always-present default-workspace fallback) are
// deliberately skipped even when their workspace_id matches: an explicit
// membership this resource created that was removed out-of-band reverts the
// default workspace to its implicit membership, and that reversion must be
// surfaced as drift (the tracked resource is gone), not mistaken for the
// membership still being present.
func findServiceAccountWorkspaceMembership(ctx context.Context, client *providerdata.OAuthClient, serviceAccountID, workspaceID string) (*anthropic.BetaServiceAccountWorkspaceMember, error) {
	iter := client.Beta.Organization.ServiceAccounts.Workspaces.ListAutoPaging(ctx, serviceAccountID, anthropic.BetaOrganizationServiceAccountWorkspaceListParams{})
	for iter.Next() {
		member := iter.Current()
		if member.WorkspaceID == workspaceID && !member.Implicit {
			return &member, nil
		}
	}
	if err := iter.Err(); err != nil {
		return nil, err
	}
	return nil, nil
}

// mapServiceAccountWorkspaceToState maps the API response to the Terraform
// state model.
func mapServiceAccountWorkspaceToState(member *anthropic.BetaServiceAccountWorkspaceMember, data *ServiceAccountWorkspaceResourceModel) {
	data.ID = types.StringValue(member.ServiceAccountID + ":" + member.WorkspaceID)
	data.ServiceAccountID = types.StringValue(member.ServiceAccountID)
	data.WorkspaceID = types.StringValue(member.WorkspaceID)
	data.WorkspaceRole = types.StringValue(string(member.WorkspaceRole))
	data.Implicit = types.BoolValue(member.Implicit)
	// The API omits the creating actor on implicit (default-workspace)
	// memberships; an empty string must surface as null, not "". The resource
	// itself only ever tracks explicit memberships, but the
	// anthropic_service_account_workspaces data source maps implicit entries
	// through this same helper.
	data.CreatedByActorID = tfvalue.StringOrNull(member.CreatedByActorID)
}
