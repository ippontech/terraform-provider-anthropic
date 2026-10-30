// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package claudetag

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
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
	_ resource.Resource                = &SlackChannelResource{}
	_ resource.ResourceWithImportState = &SlackChannelResource{}
)

// Slack's own IDs (guide, IDs): a channel ID starts with C or G, and any
// Slack ID has only capital letters and digits after its first letter and at
// most 32 characters; anything else is a 400.
var (
	slackChannelIDPattern   = regexp.MustCompile(`^[CG][A-Z0-9]{1,31}$`)
	slackWorkspaceIDPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,31}$`)
)

// slackChannelModel is the `slack` attribute of a channel: Slack's own IDs,
// which the API's bodies and responses always hold under `slack` (guide,
// IDs). The provider follows that shape, so a top-level slack_channel_id or
// slack_workspace_id is always the API's slch_ or slws_ ID, as in those
// bodies (anthropic_claude_tag_bundle_target).
type slackChannelModel struct {
	WorkspaceID types.String `tfsdk:"workspace_id"`
	ChannelID   types.String `tfsdk:"channel_id"`
}

// slackWorkspaceModel is the `slack` attribute of a workspace.
type slackWorkspaceModel struct {
	WorkspaceID types.String `tfsdk:"workspace_id"`
}

// slackChannelOf is the `slack` attribute of a channel object.
func slackChannelOf(c *slackChannelAPIObject) *slackChannelModel {
	return &slackChannelModel{WorkspaceID: types.StringValue(c.Slack.WorkspaceID), ChannelID: types.StringValue(c.Slack.ChannelID)}
}

func NewSlackChannelResource() resource.Resource {
	return &SlackChannelResource{}
}

// SlackChannelResource manages the Claude Tag settings of one Slack channel.
type SlackChannelResource struct {
	client *admin.Client
}

type SlackChannelResourceModel struct {
	ID    types.String       `tfsdk:"id"`
	Slack *slackChannelModel `tfsdk:"slack"`
	settingsModel
	CreatedAt types.String `tfsdk:"created_at"`
	UpdatedAt types.String `tfsdk:"updated_at"`
}

func (r *SlackChannelResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_claude_tag_slack_channel"
}

func (r *SlackChannelResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	attrs := map[string]schema.Attribute{
		"id": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "The channel's Claude Tag ID (prefixed `slch_`).",
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
		"slack": schema.SingleNestedAttribute{
			Required: true,
			MarkdownDescription: "Slack's own IDs of the channel, the ones Slack shows (the API's `slack` object). Changing " +
				"either sets up another channel.",
			PlanModifiers: []planmodifier.Object{objectplanmodifier.RequiresReplace()},
			Attributes: map[string]schema.Attribute{
				"workspace_id": schema.StringAttribute{
					Required: true,
					MarkdownDescription: "Slack's own ID of the workspace the channel is in (`T...`), or of an Enterprise " +
						"Grid (`E...`) for a channel shared with the whole Grid. Not the `slws_` ID.",
					Validators: []validator.String{stringvalidator.RegexMatches(slackWorkspaceIDPattern,
						"must be Slack's own workspace ID (T... or E..., capital letters and digits, at most 32 characters)")},
				},
				"channel_id": schema.StringAttribute{
					Required:            true,
					MarkdownDescription: "Slack's own ID of the channel (`C...` or `G...`). Not the `slch_` ID.",
					Validators: []validator.String{stringvalidator.RegexMatches(slackChannelIDPattern,
						"must be Slack's own channel ID (C... or G..., capital letters and digits, at most 32 characters)")},
				},
			},
		},
		"created_at": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "RFC 3339 timestamp of when the channel was set up.",
			PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
		},
		"updated_at": schema.StringAttribute{
			Computed: true,
			MarkdownDescription: "RFC 3339 timestamp of the last change to Claude Tag's record of the channel, here, in " +
				"claude.ai or in Slack. Attaching or detaching a Bundle does not move it.",
		},
	}
	maps.Copy(attrs, settingsResourceAttributes("channel"))
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages the Claude Tag settings of one Slack channel (Claude Tag API, beta, Claude Enterprise). " +
			"Creating it sets the channel up, or adopts it when it is already set up; destroying it removes the channel " +
			"from Claude Tag, which Claude Tag allows only while Claude is off in it." + claudeTagAuth,
		Attributes: attrs,
	}
}

func (r *SlackChannelResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if client := configureResource(req, resp); client != nil {
		r.client = client
	}
}

// Create follows the guide's version-control workflow: it finds the channel
// by its Slack IDs, compares what the channel stores with the plan, and sets
// the channel up with only the settings that differ (null for one it stores
// and the plan does not). Setting a channel up "returns the existing channel
// and applies settings" when it is already set up, so an existing channel is
// adopted; when it already stores the plan, nothing is sent.
func (r *SlackChannelResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan SlackChannelResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	slack := slackIDs{WorkspaceID: plan.Slack.WorkspaceID.ValueString(), ChannelID: plan.Slack.ChannelID.ValueString()}
	existing, err := findSlackChannel(ctx, r.client, slack)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", errorDetail("look up Claude Tag channel "+slack.ChannelID, err))
		return
	}

	stored := nullSettings()
	if existing != nil {
		stored = settingsFromAPI(existing.Settings, plan.settingsModel)
	}
	patch := settingsPatch(plan.settingsModel, stored)
	if msg := runnerPoolConflict(patch, stored, plan.settingsModel, "channel"); msg != "" {
		resp.Diagnostics.AddAttributeError(path.Root("environment_id"), "Runner Pool Cannot Be Changed", msg)
		return
	}

	channel := existing
	if existing == nil || len(patch) > 0 {
		body := map[string]any{"slack": slack}
		if len(patch) > 0 {
			body["settings"] = patch
		}
		channel = &slackChannelAPIObject{}
		if err := callJSON(ctx, r.client, http.MethodPost, slackChannelsPath, nil, body, channel); err != nil {
			resp.Diagnostics.AddError("Client Error", errorDetail("set up Claude Tag channel "+slack.ChannelID, err)+setUpNotFoundHint(err))
			return
		}
	}

	mapSlackChannel(channel, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *SlackChannelResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state SlackChannelResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var channel slackChannelAPIObject
	if err := callJSON(ctx, r.client, http.MethodGet, slackChannelPath(state.ID.ValueString()), nil, nil, &channel); err != nil {
		gone, diags := readFailure(ctx, r.client, "Claude Tag channel "+state.ID.ValueString(), err)
		if gone {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.Append(diags...)
		return
	}

	mapSlackChannel(&channel, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update sends only the settings that changed, and null for one the
// configuration no longer declares.
func (r *SlackChannelResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state SlackChannelResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	channel, diags := updateSettings[slackChannelAPIObject](ctx, r.client, slackChannelPath(state.ID.ValueString()),
		plan.settingsModel, state.settingsModel, "channel")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if channel == nil {
		// Only the white space around instructions changed, which the API
		// does not store: nothing to send.
		state.settingsModel = plan.settingsModel
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		return
	}

	mapSlackChannel(channel, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Delete removes the channel from Claude Tag. A 404 is a channel already
// gone. While Claude is on in the channel the API refuses with
// slack_channel_enabled, and the diagnostic says how to proceed.
func (r *SlackChannelResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state SlackChannelResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	err := callJSON(ctx, r.client, http.MethodDelete, slackChannelPath(state.ID.ValueString()), nil, nil, nil)
	if err == nil || admin.IsNotFound(err) {
		return
	}
	detail := errorDetail("remove Claude Tag channel "+state.ID.ValueString(), err)
	if status, details := apiErrorOf(err); status == http.StatusBadRequest && details.ErrorCode == "" {
		detail += "\n\nA group direct message, and a channel holding configuration the API does not manage (a runner " +
			"pool, or a setting only claude.ai has), cannot be removed through the API: an organization owner removes " +
			"the second kind in claude.ai. To stop managing the channel without removing it, use a removed block or " +
			"terraform state rm."
	}
	resp.Diagnostics.AddError("Client Error", detail)
}

// ImportState takes the channel's `slch_` ID.
func (r *SlackChannelResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if !strings.HasPrefix(req.ID, "slch_") {
		resp.Diagnostics.AddError("Invalid import ID",
			fmt.Sprintf("Expected the channel's Claude Tag ID (prefixed slch_), not Slack's own channel ID; got %q.", req.ID))
		return
	}
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// findSlackChannel looks a channel up by its Slack IDs, nil when it is not
// set up. In a workspace the organization owns only through an Enterprise
// Grid and that is not set up yet, the lookup answers 404 instead of an
// empty list (guide, Find a channel by its Slack IDs); setting the channel up
// is what sets such a workspace up, so that 404 is a channel to set up too.
func findSlackChannel(ctx context.Context, client *admin.Client, slack slackIDs) (*slackChannelAPIObject, error) {
	q := url.Values{"slack_workspace_id": {slack.WorkspaceID}, "slack_channel_id": {slack.ChannelID}}
	channel, err := lookup[slackChannelAPIObject](ctx, client, slackChannelsPath, q)
	if status, details := apiErrorOf(err); status == http.StatusNotFound && details.ErrorCode == "" {
		return nil, nil
	}
	return channel, err
}

// setUpNotFoundHint explains the 404 of a setup request without an
// error_code (guide, Set up a channel).
func setUpNotFoundHint(err error) string {
	if status, details := apiErrorOf(err); status != http.StatusNotFound || details.ErrorCode != "" {
		return ""
	}
	return "\n\nBefore a channel is set up, Claude's Slack app confirms with Slack that the channel is in the workspace, " +
		"is not archived and is visible to the app; the workspace must have the app installed (on the Grid, for a " +
		"workspace the organization owns through an Enterprise Grid) and belong to the organization."
}

// updateSettings sends the change from stored to plan to the update endpoint
// at path and returns the object it answers, or nil when nothing differs.
func updateSettings[T any](ctx context.Context, client *admin.Client, objectPath string, plan, stored settingsModel, level string) (*T, diag.Diagnostics) {
	var diags diag.Diagnostics
	patch := settingsPatch(plan, stored)
	if len(patch) == 0 {
		return nil, diags
	}
	if msg := runnerPoolConflict(patch, stored, plan, level); msg != "" {
		diags.AddAttributeError(path.Root("environment_id"), "Runner Pool Cannot Be Changed", msg)
		return nil, diags
	}
	out := new(T)
	if err := callJSON(ctx, client, http.MethodPost, objectPath, nil, map[string]any{"settings": patch}, out); err != nil {
		diags.AddError("Client Error", errorDetail("update the Claude Tag settings of the "+level, err))
		return nil, diags
	}
	return out, diags
}

func mapSlackChannel(c *slackChannelAPIObject, data *SlackChannelResourceModel) {
	data.ID = types.StringValue(c.ID)
	data.Slack = slackChannelOf(c)
	data.settingsModel = settingsFromAPI(c.Settings, data.settingsModel)
	data.CreatedAt = tfvalue.StringOrNull(c.CreatedAt)
	data.UpdatedAt = tfvalue.StringOrNull(c.UpdatedAt)
}
