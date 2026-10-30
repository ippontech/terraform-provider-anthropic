// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package claudetag

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
)

var (
	_ resource.Resource                     = &BundleTargetResource{}
	_ resource.ResourceWithImportState      = &BundleTargetResource{}
	_ resource.ResourceWithConfigValidators = &BundleTargetResource{}
)

// The IDs this API issues (guide, IDs).
var (
	bundleIDPattern          = regexp.MustCompile(`^capp_[A-Za-z0-9]+$`)
	bundleTargetIDPattern    = regexp.MustCompile(`^btgt_[A-Za-z0-9]+$`)
	slackChannelRefPattern   = regexp.MustCompile(`^slch_[A-Za-z0-9]+$`)
	slackWorkspaceRefPattern = regexp.MustCompile(`^slws_[A-Za-z0-9]+$`)
)

func NewBundleTargetResource() resource.Resource {
	return &BundleTargetResource{}
}

// BundleTargetResource attaches an Access bundle to a set-up channel or
// workspace. A target has no update endpoint: every argument forces a
// replacement.
type BundleTargetResource struct {
	client *admin.Client
}

type BundleTargetResourceModel struct {
	ID               types.String `tfsdk:"id"`
	BundleID         types.String `tfsdk:"bundle_id"`
	SlackChannelID   types.String `tfsdk:"slack_channel_id"`
	SlackWorkspaceID types.String `tfsdk:"slack_workspace_id"`
	Type             types.String `tfsdk:"type"`
	CreatedAt        types.String `tfsdk:"created_at"`
}

func (r *BundleTargetResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_claude_tag_bundle_target"
}

func (r *BundleTargetResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	replace := []planmodifier.String{stringplanmodifier.RequiresReplace()}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Attaches a Claude Tag Access bundle to a Slack channel or a Slack workspace that is set up " +
			"(Claude Tag API, beta, Claude Enterprise). A Bundle attached to a workspace can be used in the workspace and " +
			"every channel in it; one attached to a channel, in that channel only. Destroying it detaches the Bundle." +
			claudeTagAuth,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The Bundle target's ID (prefixed `btgt_`). The same pair always has the same ID.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"bundle_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "ID of the Access bundle (prefixed `capp_`), from `anthropic_claude_tag_bundles`.",
				PlanModifiers:       replace,
				Validators: []validator.String{stringvalidator.RegexMatches(bundleIDPattern,
					"must be a Bundle ID (prefixed capp_)")},
			},
			"slack_channel_id": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Claude Tag ID (prefixed `slch_`) of the channel to attach the Bundle to, such as " +
					"`anthropic_claude_tag_slack_channel.<name>.id`; not Slack's own channel ID. Exactly one of " +
					"`slack_channel_id` and `slack_workspace_id`.",
				PlanModifiers: replace,
				Validators: []validator.String{stringvalidator.RegexMatches(slackChannelRefPattern,
					"must be a channel's Claude Tag ID (prefixed slch_), not Slack's own channel ID")},
			},
			"slack_workspace_id": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Claude Tag ID (prefixed `slws_`) of the workspace to attach the Bundle to, such as " +
					"`anthropic_claude_tag_slack_workspace_settings.<name>.id`; not Slack's own workspace ID.",
				PlanModifiers: replace,
				Validators: []validator.String{stringvalidator.RegexMatches(slackWorkspaceRefPattern,
					"must be a workspace's Claude Tag ID (prefixed slws_), not Slack's own workspace ID")},
			},
			"type": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "`slack_channel` or `slack_workspace`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the Bundle was attached.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

func (r *BundleTargetResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		resourcevalidator.ExactlyOneOf(path.MatchRoot("slack_channel_id"), path.MatchRoot("slack_workspace_id")),
	}
}

func (r *BundleTargetResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if client := configureResource(req, resp); client != nil {
		r.client = client
	}
}

// Create attaches the Bundle. "Attaching a pair that is already attached
// returns 200 with the existing Bundle target", so an attachment made in
// claude.ai is adopted.
func (r *BundleTargetResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan BundleTargetResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body := map[string]any{"type": "slack_workspace", "slack_workspace_id": plan.SlackWorkspaceID.ValueString()}
	if !plan.SlackChannelID.IsNull() {
		body = map[string]any{"type": "slack_channel", "slack_channel_id": plan.SlackChannelID.ValueString()}
	}
	var target bundleTargetAPIObject
	if err := callJSON(ctx, r.client, http.MethodPost, bundleTargetsPath(plan.BundleID.ValueString()), nil, body, &target); err != nil {
		detail := errorDetail("attach Claude Tag bundle "+plan.BundleID.ValueString(), err)
		if admin.IsNotFound(err) {
			detail += "\n\nThe Bundle must exist, and the channel or workspace must be set up: attaching never sets a channel up."
		}
		resp.Diagnostics.AddError("Client Error", detail)
		return
	}

	mapBundleTarget(&target, &plan)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// Read gets the attachment. A 404 means it was detached, its Bundle deleted,
// or its channel or workspace is no longer set up (guide, Get a Bundle
// target): the next apply attaches it again.
func (r *BundleTargetResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state BundleTargetResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var target bundleTargetAPIObject
	p := bundleTargetPath(state.BundleID.ValueString(), state.ID.ValueString())
	if err := callJSON(ctx, r.client, http.MethodGet, p, nil, nil, &target); err != nil {
		gone, diags := readFailure(ctx, r.client, "Claude Tag bundle target "+state.ID.ValueString(), err)
		if gone {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.Append(diags...)
		return
	}

	mapBundleTarget(&target, &state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// Update is never called: every argument forces a replacement.
func (r *BundleTargetResource) Update(_ context.Context, _ resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Unexpected Update", "Every argument of anthropic_claude_tag_bundle_target forces a replacement. "+
		"Please report this issue to the provider developers.")
}

// Delete detaches the Bundle; a 404 is an attachment already gone.
func (r *BundleTargetResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state BundleTargetResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	p := bundleTargetPath(state.BundleID.ValueString(), state.ID.ValueString())
	if err := callJSON(ctx, r.client, http.MethodDelete, p, nil, nil, nil); err != nil && !admin.IsNotFound(err) {
		resp.Diagnostics.AddError("Client Error", errorDetail("detach Claude Tag bundle "+state.BundleID.ValueString(), err))
	}
}

// ImportState takes `<bundle_id>:<target_id>`, the separator of the
// provider's other composite import IDs: a target's paths need its Bundle's
// ID, which the target does not hold.
func (r *BundleTargetResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	bundleID, targetID, ok := strings.Cut(req.ID, ":")
	if !ok || !bundleIDPattern.MatchString(bundleID) || !bundleTargetIDPattern.MatchString(targetID) {
		resp.Diagnostics.AddError("Invalid import ID",
			fmt.Sprintf("Expected <bundle_id>:<target_id> (capp_...:btgt_...); got %q.", req.ID))
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("bundle_id"), bundleID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), targetID)...)
}

func mapBundleTarget(t *bundleTargetAPIObject, data *BundleTargetResourceModel) {
	data.ID = types.StringValue(t.ID)
	data.Type = tfvalue.StringOrNull(t.Type)
	data.SlackChannelID = tfvalue.StringOrNull(t.SlackChannelID)
	data.SlackWorkspaceID = tfvalue.StringOrNull(t.SlackWorkspaceID)
	data.CreatedAt = tfvalue.StringOrNull(t.CreatedAt)
}
