// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

var _ datasource.DataSource = &WorkspaceMemberDataSource{}

func NewWorkspaceMemberDataSource() datasource.DataSource {
	return &WorkspaceMemberDataSource{}
}

type WorkspaceMemberDataSource struct {
	adminClient *admin.Client
}

type WorkspaceMemberDataSourceModel struct {
	WorkspaceID   types.String `tfsdk:"workspace_id"`
	UserID        types.String `tfsdk:"user_id"`
	WorkspaceRole types.String `tfsdk:"workspace_role"`
	Type          types.String `tfsdk:"type"`
}

type workspaceMemberAPIResponse struct {
	Type          string `json:"type"`
	WorkspaceRole string `json:"workspace_role"`
}

func (d *WorkspaceMemberDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_workspace_member"
}

func (d *WorkspaceMemberDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fetches a single Anthropic workspace member by workspace ID and user ID. " +
			"Requires `admin_api_key` (or `ANTHROPIC_ADMIN_API_KEY`) to be configured on the provider.",
		Attributes: map[string]schema.Attribute{
			"workspace_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the workspace.",
			},
			"user_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the user.",
			},
			"workspace_role": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The role of the user in the workspace (e.g. `workspace_developer`, `workspace_admin`).",
			},
			"type": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Object type. Always `workspace_member`.",
			},
		},
	}
}

func (d *WorkspaceMemberDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

	if !providerrors.RequireAdminDataSourceClient(pd.AdminClient, &resp.Diagnostics) {
		return
	}

	d.adminClient = pd.AdminClient
}

func (d *WorkspaceMemberDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data WorkspaceMemberDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	path := fmt.Sprintf("/v1/organizations/workspaces/%s/members/%s", data.WorkspaceID.ValueString(), data.UserID.ValueString())
	respBytes, err := d.adminClient.DoRequest(ctx, "GET", path, nil)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read workspace member: %s", err))
		return
	}

	var member workspaceMemberAPIResponse
	if err := json.Unmarshal(respBytes, &member); err != nil {
		resp.Diagnostics.AddError("Parse Error", fmt.Sprintf("Unable to parse workspace member response: %s", err))
		return
	}

	data.WorkspaceRole = types.StringValue(member.WorkspaceRole)
	data.Type = types.StringValue(member.Type)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
