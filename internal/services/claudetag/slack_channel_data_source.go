// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package claudetag

import (
	"context"
	"fmt"
	"maps"
	"net/http"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
)

var (
	_ datasource.DataSource                     = &SlackChannelDataSource{}
	_ datasource.DataSourceWithConfigValidators = &SlackChannelDataSource{}
)

func NewSlackChannelDataSource() datasource.DataSource {
	return &SlackChannelDataSource{}
}

// SlackChannelDataSource reads one set-up channel, by its `slch_` ID or by
// its Slack IDs, with what it stores or, with include_inherited, what
// applies in it.
type SlackChannelDataSource struct {
	client *admin.Client
}

type SlackChannelDataSourceModel struct {
	ID               types.String       `tfsdk:"id"`
	Slack            *slackChannelModel `tfsdk:"slack"`
	IncludeInherited types.Bool         `tfsdk:"include_inherited"`
	settingsModel
	CreatedAt types.String `tfsdk:"created_at"`
	UpdatedAt types.String `tfsdk:"updated_at"`
}

// includeInheritedAttribute is the switch between the two readings (guide,
// Stored and inherited values).
func includeInheritedAttribute(level string) schema.BoolAttribute {
	return schema.BoolAttribute{
		Optional: true,
		MarkdownDescription: "`true` to read what applies in the " + level + " once every level (channel, workspace, Slack " +
			"defaults) is taken into account, instead of what the " + level + " stores. Default: `false`. With `true`, " +
			"`enabled` and `guest_policy` are never null, and a level that cannot be read fails the read (503).",
	}
}

func (d *SlackChannelDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_claude_tag_slack_channel"
}

func (d *SlackChannelDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attrs := map[string]schema.Attribute{
		"id": schema.StringAttribute{
			Optional: true, Computed: true,
			MarkdownDescription: "The channel's Claude Tag ID (prefixed `slch_`). Set it, or `slack`.",
			Validators: []validator.String{stringvalidator.RegexMatches(slackChannelRefPattern,
				"must be a channel's Claude Tag ID (prefixed slch_)")},
		},
		"slack": schema.SingleNestedAttribute{
			Optional: true, Computed: true,
			MarkdownDescription: "Slack's own IDs of the channel, the ones Slack shows (the API's `slack` object). Set it, " +
				"or `id`.",
			Attributes: map[string]schema.Attribute{
				"workspace_id": schema.StringAttribute{
					Required:            true,
					MarkdownDescription: "Slack's own ID of the channel's workspace (`T...`, or `E...` for a channel stored under a Grid).",
					Validators: []validator.String{stringvalidator.RegexMatches(slackWorkspaceIDPattern,
						"must be Slack's own workspace ID")},
				},
				"channel_id": schema.StringAttribute{
					Required:            true,
					MarkdownDescription: "Slack's own ID of the channel (`C...` or `G...`).",
					Validators: []validator.String{stringvalidator.RegexMatches(slackChannelIDPattern,
						"must be Slack's own channel ID")},
				},
			},
		},
		"include_inherited": includeInheritedAttribute("channel"),
		"created_at":        schema.StringAttribute{Computed: true, MarkdownDescription: "RFC 3339 timestamp of when the channel was set up."},
		"updated_at":        schema.StringAttribute{Computed: true, MarkdownDescription: "RFC 3339 timestamp of the last change to Claude Tag's record of the channel."},
	}
	maps.Copy(attrs, settingsDataSourceAttributes("channel"))
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads the Claude Tag settings of one set-up Slack channel (Claude Tag API, beta, Claude " +
			"Enterprise), by its Claude Tag ID or by its Slack IDs: what the channel stores, or what applies in it." + claudeTagAuth,
		Attributes: attrs,
	}
}

func (d *SlackChannelDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{
		datasourcevalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("slack")),
	}
}

func (d *SlackChannelDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if client := configureDataSource(req, resp); client != nil {
		d.client = client
	}
}

func (d *SlackChannelDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data SlackChannelDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	q := inheritedQuery(data.IncludeInherited.ValueBool())
	var channel *slackChannelAPIObject
	var err error
	what := "channel " + data.ID.ValueString()
	if !data.ID.IsNull() {
		channel = &slackChannelAPIObject{}
		err = callJSON(ctx, d.client, http.MethodGet, slackChannelPath(data.ID.ValueString()), q, nil, channel)
	} else {
		what = fmt.Sprintf("channel %s of Slack workspace %s", data.Slack.ChannelID.ValueString(), data.Slack.WorkspaceID.ValueString())
		q.Set("slack_workspace_id", data.Slack.WorkspaceID.ValueString())
		q.Set("slack_channel_id", data.Slack.ChannelID.ValueString())
		channel, err = lookup[slackChannelAPIObject](ctx, d.client, slackChannelsPath, q)
	}
	switch {
	case admin.IsNotFound(err):
		resp.Diagnostics.AddError("Claude Tag Channel Not Found", fmt.Sprintf("No set-up Claude Tag %s in the organization: "+
			"the channel or its workspace is not the organization's, or the Claude Tag API is not enabled for it. %s", what, err))
		return
	case err != nil:
		resp.Diagnostics.AddError("Client Error", errorDetail("read Claude Tag "+what, err))
		return
	case channel == nil:
		resp.Diagnostics.AddError("Claude Tag Channel Not Found", fmt.Sprintf("Slack %s is not set up in Claude Tag.", what))
		return
	}

	data.ID = types.StringValue(channel.ID)
	data.Slack = slackChannelOf(channel)
	data.settingsModel = settingsFromAPI(channel.Settings, nullSettings())
	data.CreatedAt = tfvalue.StringOrNull(channel.CreatedAt)
	data.UpdatedAt = tfvalue.StringOrNull(channel.UpdatedAt)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
