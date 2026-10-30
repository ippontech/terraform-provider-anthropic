// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package claudetag

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
)

var (
	_ resource.Resource                = &SlackWorkspaceSettingsResource{}
	_ resource.ResourceWithImportState = &SlackWorkspaceSettingsResource{}
)

func NewSlackWorkspaceSettingsResource() resource.Resource {
	return &SlackWorkspaceSettingsResource{}
}

// SlackWorkspaceSettingsResource manages the Claude Tag settings of a Slack
// workspace that is already set up. Workspaces are connected in claude.ai and
// no endpoint sets one up or removes one (guide, Set-up channels and
// workspaces), so Create adopts the workspace and Delete clears its settings:
// the resource neither creates nor removes the workspace, hence `_settings`.
type SlackWorkspaceSettingsResource struct {
	client *admin.Client
}

type SlackWorkspaceSettingsResourceModel struct {
	ID    types.String         `tfsdk:"id"`
	Slack *slackWorkspaceModel `tfsdk:"slack"`
	settingsModel
	CreatedAt types.String `tfsdk:"created_at"`
	UpdatedAt types.String `tfsdk:"updated_at"`
}

func (r *SlackWorkspaceSettingsResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_claude_tag_slack_workspace_settings"
}

func (r *SlackWorkspaceSettingsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	attrs := map[string]schema.Attribute{
		"id": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "The workspace's Claude Tag ID (prefixed `slws_`).",
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
		"slack": schema.SingleNestedAttribute{
			Required: true,
			MarkdownDescription: "Slack's own ID of the workspace (the API's `slack` object). The workspace must be set up " +
				"in Claude Tag already. Changing it manages another workspace.",
			PlanModifiers: []planmodifier.Object{objectplanmodifier.RequiresReplace()},
			Attributes: map[string]schema.Attribute{
				"workspace_id": schema.StringAttribute{
					Required: true,
					MarkdownDescription: "Slack's own ID of the workspace (`T...`; Slack's API calls it a team ID). Not the " +
						"`slws_` ID.",
					Validators: []validator.String{stringvalidator.RegexMatches(slackWorkspaceIDPattern,
						"must be Slack's own workspace ID (T..., capital letters and digits, at most 32 characters)")},
				},
			},
		},
		"created_at": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "RFC 3339 timestamp of when the workspace was set up.",
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
		"updated_at": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "RFC 3339 timestamp of the last change to Claude Tag's record of the workspace.",
		},
	}
	maps.Copy(attrs, settingsResourceAttributes("workspace"))
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages the Claude Tag settings of a Slack workspace already connected in claude.ai (Claude " +
			"Tag API, beta, Claude Enterprise); it neither connects nor disconnects the workspace. The settings apply in " +
			"every channel of the workspace that does not set its own value, except instructions, which channels add to. " +
			"Destroying it clears the settings, so the workspace inherits them from the organization's Slack defaults; the " +
			"workspace stays connected." + claudeTagAuth,
		Attributes: attrs,
	}
}

func (r *SlackWorkspaceSettingsResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if client := configureResource(req, resp); client != nil {
		r.client = client
	}
}

// Create finds the workspace by Slack's ID and sends only the settings that
// differ from what it stores, null for one the plan does not set.
func (r *SlackWorkspaceSettingsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan SlackWorkspaceSettingsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	slackID := plan.Slack.WorkspaceID.ValueString()
	q := url.Values{"slack_workspace_id": {slackID}}
	workspace, err := lookup[slackWorkspaceAPIObject](ctx, r.client, slackWorkspacesPath, q)
	switch {
	case admin.IsNotFound(err):
		resp.Diagnostics.AddAttributeError(path.Root("slack").AtName("workspace_id"), "Slack Workspace Not Found", fmt.Sprintf(
			"The Claude Tag API answers 404 for Slack workspace %s: the organization does not own it, or owns it only "+
				"through an Enterprise Grid and it is not set up yet (setting up one of its channels, with "+
				"anthropic_claude_tag_slack_channel, sets it up). %s", slackID, err))
		return
	case err != nil:
		resp.Diagnostics.AddError("Client Error", errorDetail("look up Slack workspace "+slackID, err))
		return
	case workspace == nil:
		resp.Diagnostics.AddAttributeError(path.Root("slack").AtName("workspace_id"), "Slack Workspace Not Set Up", fmt.Sprintf(
			"Slack workspace %s is not set up in Claude Tag. An organization owner connects workspaces in claude.ai "+
				"(Organization settings > Claude Tag); the API cannot.", slackID))
		return
	}

	stored := settingsFromAPI(workspace.Settings, plan.settingsModel)
	updated, diags := updateSettings[slackWorkspaceAPIObject](ctx, r.client, slackWorkspacePath(workspace.ID),
		plan.settingsModel, stored, "workspace")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if updated != nil {
		workspace = updated
	}

	mapSlackWorkspaceSettings(workspace, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *SlackWorkspaceSettingsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state SlackWorkspaceSettingsResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var workspace slackWorkspaceAPIObject
	if err := callJSON(ctx, r.client, http.MethodGet, slackWorkspacePath(state.ID.ValueString()), nil, nil, &workspace); err != nil {
		gone, diags := readFailure(ctx, r.client, "Claude Tag workspace "+state.ID.ValueString(), err)
		if gone {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.Append(diags...)
		return
	}

	mapSlackWorkspaceSettings(&workspace, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *SlackWorkspaceSettingsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state SlackWorkspaceSettingsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	workspace, diags := updateSettings[slackWorkspaceAPIObject](ctx, r.client, slackWorkspacePath(state.ID.ValueString()),
		plan.settingsModel, state.settingsModel, "workspace")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if workspace == nil {
		state.settingsModel = plan.settingsModel
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		return
	}

	mapSlackWorkspaceSettings(workspace, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete sends null for every setting the workspace stores, which restores
// inheritance from the Slack defaults; the workspace stays connected, since
// no endpoint removes one. A runner pool, which the API cannot clear, stays,
// with a warning. A 404 is a workspace already gone.
func (r *SlackWorkspaceSettingsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state SlackWorkspaceSettingsResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	patch, kept := clearAllPatch(state.settingsModel)
	if kept != "" {
		resp.Diagnostics.AddWarning("Runner Pool Kept", fmt.Sprintf(
			"The workspace keeps the self-hosted runner pool %s as environment_id: the Claude Tag API cannot clear a "+
				"runner pool. An organization owner changes it in claude.ai.", kept))
	}
	if len(patch) == 0 {
		return
	}
	err := callJSON(ctx, r.client, http.MethodPost, slackWorkspacePath(state.ID.ValueString()), nil,
		map[string]any{"settings": patch}, nil)
	if err != nil && !admin.IsNotFound(err) {
		resp.Diagnostics.AddError("Client Error", errorDetail("clear the Claude Tag settings of workspace "+state.ID.ValueString(), err))
	}
}

// ImportState takes the workspace's `slws_` ID.
func (r *SlackWorkspaceSettingsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !strings.HasPrefix(req.ID, "slws_") {
		resp.Diagnostics.AddError("Invalid import ID",
			fmt.Sprintf("Expected the workspace's Claude Tag ID (prefixed slws_), not Slack's own workspace ID; got %q.", req.ID))
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func mapSlackWorkspaceSettings(w *slackWorkspaceAPIObject, data *SlackWorkspaceSettingsResourceModel) {
	data.ID = types.StringValue(w.ID)
	data.Slack = &slackWorkspaceModel{WorkspaceID: types.StringValue(w.Slack.WorkspaceID)}
	data.settingsModel = settingsFromAPI(w.Settings, data.settingsModel)
	data.CreatedAt = tfvalue.StringOrNull(w.CreatedAt)
	data.UpdatedAt = tfvalue.StringOrNull(w.UpdatedAt)
}
