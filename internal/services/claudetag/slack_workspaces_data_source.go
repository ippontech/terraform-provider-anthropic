// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package claudetag

import (
	"context"
	"fmt"
	"maps"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
)

var _ datasource.DataSource = &SlackWorkspacesDataSource{}

func NewSlackWorkspacesDataSource() datasource.DataSource {
	return &SlackWorkspacesDataSource{}
}

// SlackWorkspacesDataSource lists the set-up Slack workspaces the
// organization owns, newest first.
type SlackWorkspacesDataSource struct {
	client *admin.Client
}

type SlackWorkspacesDataSourceModel struct {
	Slack            *slackWorkspaceModel `tfsdk:"slack"`
	IncludeInherited types.Bool           `tfsdk:"include_inherited"`
	SlackWorkspaces  types.List           `tfsdk:"slack_workspaces"`
}

// slackWorkspaceSlackAttrTypes is the `slack` object of a listed workspace.
var slackWorkspaceSlackAttrTypes = map[string]attr.Type{"workspace_id": types.StringType}

var slackWorkspaceAttrTypes = map[string]attr.Type{
	"id":             types.StringType,
	"slack":          types.ObjectType{AttrTypes: slackWorkspaceSlackAttrTypes},
	"enabled":        types.BoolType,
	"instructions":   types.StringType,
	"model":          types.StringType,
	"environment_id": types.StringType,
	"guest_policy":   types.StringType,
	"created_at":     types.StringType,
	"updated_at":     types.StringType,
}

func (d *SlackWorkspacesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_claude_tag_slack_workspaces"
}

func (d *SlackWorkspacesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	item := map[string]schema.Attribute{
		"id": schema.StringAttribute{Computed: true, MarkdownDescription: "The workspace's Claude Tag ID (prefixed `slws_`)."},
		"slack": schema.SingleNestedAttribute{
			Computed:            true,
			MarkdownDescription: "Slack's own ID of the workspace (the API's `slack` object).",
			Attributes: map[string]schema.Attribute{
				"workspace_id": schema.StringAttribute{Computed: true, MarkdownDescription: "Slack's own ID of the workspace (`T...`)."},
			},
		},
		"created_at": schema.StringAttribute{Computed: true, MarkdownDescription: "RFC 3339 timestamp of when the workspace was set up."},
		"updated_at": schema.StringAttribute{Computed: true, MarkdownDescription: "RFC 3339 timestamp of the last change to Claude Tag's record of the workspace."},
	}
	maps.Copy(item, settingsDataSourceAttributes("workspace"))
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the Slack workspaces set up in Claude Tag that the organization owns, newest first " +
			"(Claude Tag API, beta, Claude Enterprise), with what each stores or, with `include_inherited`, what applies " +
			"in it. All pages are fetched." + claudeTagAuth,
		Attributes: map[string]schema.Attribute{
			"slack": schema.SingleNestedAttribute{
				Optional: true,
				MarkdownDescription: "Only the workspace with this Slack ID: the list then holds it, or nothing when it is not " +
					"set up. A workspace the organization does not own is an error.",
				Attributes: map[string]schema.Attribute{
					"workspace_id": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "Slack's own ID of the workspace (`T...`). Not the `slws_` ID.",
						Validators: []validator.String{stringvalidator.RegexMatches(slackWorkspaceIDPattern,
							"must be Slack's own workspace ID")},
					},
				},
			},
			"include_inherited": includeInheritedAttribute("workspace"),
			"slack_workspaces": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "The workspaces, newest first.",
				NestedObject:        schema.NestedAttributeObject{Attributes: item},
			},
		},
	}
}

func (d *SlackWorkspacesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if client := configureDataSource(req, resp); client != nil {
		d.client = client
	}
}

func (d *SlackWorkspacesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data SlackWorkspacesDataSourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	q := inheritedQuery(data.IncludeInherited.ValueBool())
	if data.Slack != nil {
		q.Set("slack_workspace_id", data.Slack.WorkspaceID.ValueString())
	}
	workspaces, err := listAll[slackWorkspaceAPIObject](ctx, d.client, slackWorkspacesPath, q)
	if err != nil {
		detail := errorDetail("list Claude Tag Slack workspaces", err)
		if admin.IsNotFound(err) && data.Slack != nil {
			detail += fmt.Sprintf("\n\nThe organization does not own Slack workspace %s, or owns it only through an "+
				"Enterprise Grid and it is not set up yet.", data.Slack.WorkspaceID.ValueString())
		}
		resp.Diagnostics.AddError("Client Error", detail)
		return
	}

	list, diags := slackWorkspacesList(workspaces)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.SlackWorkspaces = list
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func slackWorkspacesList(workspaces []slackWorkspaceAPIObject) (types.List, diag.Diagnostics) {
	var diags diag.Diagnostics
	elemType := types.ObjectType{AttrTypes: slackWorkspaceAttrTypes}
	elems := make([]attr.Value, 0, len(workspaces))
	for _, w := range workspaces {
		s := settingsFromAPI(w.Settings, nullSettings())
		slack, d := types.ObjectValue(slackWorkspaceSlackAttrTypes, map[string]attr.Value{
			"workspace_id": types.StringValue(w.Slack.WorkspaceID),
		})
		diags.Append(d...)
		obj, d := types.ObjectValue(slackWorkspaceAttrTypes, map[string]attr.Value{
			"id":             types.StringValue(w.ID),
			"slack":          slack,
			"enabled":        s.Enabled,
			"instructions":   s.Instructions,
			"model":          s.Model,
			"environment_id": s.EnvironmentID,
			"guest_policy":   s.GuestPolicy,
			"created_at":     tfvalue.StringOrNull(w.CreatedAt),
			"updated_at":     tfvalue.StringOrNull(w.UpdatedAt),
		})
		diags.Append(d...)
		elems = append(elems, obj)
	}
	if diags.HasError() {
		return types.ListNull(elemType), diags
	}
	list, d := types.ListValue(elemType, elems)
	diags.Append(d...)
	return list, diags
}
