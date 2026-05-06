// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &WorkspaceMembersDataSource{}

func NewWorkspaceMembersDataSource() datasource.DataSource {
	return &WorkspaceMembersDataSource{}
}

// WorkspaceMembersDataSource defines the data source implementation.
type WorkspaceMembersDataSource struct {
	adminClient *admin.Client
}

// WorkspaceMembersDataSourceModel describes the data source data model.
type WorkspaceMembersDataSourceModel struct {
	WorkspaceID types.String `tfsdk:"workspace_id"`
	Members     types.List   `tfsdk:"members"`
}

var workspaceMemberAttrTypes = map[string]attr.Type{
	"user_id":        types.StringType,
	"workspace_id":   types.StringType,
	"workspace_role": types.StringType,
	"type":           types.StringType,
}

// --- Admin API response types ---

type workspaceMembersListItem struct {
	UserID        string `json:"user_id"`
	WorkspaceID   string `json:"workspace_id"`
	WorkspaceRole string `json:"workspace_role"`
	Type          string `json:"type"`
}

type workspaceMembersListResponse struct {
	Data    []workspaceMembersListItem `json:"data"`
	HasMore bool                         `json:"has_more"`
	LastID  string                       `json:"last_id"`
	FirstID string                       `json:"first_id"`
}

// --- Schema ---

func (d *WorkspaceMembersDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_workspace_members"
}

func (d *WorkspaceMembersDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists all members of an Anthropic Workspace via the Admin API. " +
			"All pages are fetched automatically. " +
			"Requires `admin_api_key` (or `ANTHROPIC_ADMIN_API_KEY`) to be configured on the provider.",
		Attributes: map[string]schema.Attribute{
			"workspace_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the workspace whose members to list.",
			},
			"members": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "List of all members in the workspace.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"user_id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "The ID of the user.",
						},
						"workspace_id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "The ID of the workspace.",
						},
						"workspace_role": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "The role of the member in the workspace (e.g. `workspace_admin`, `workspace_developer`, `workspace_billing`).",
						},
						"type": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Object type. Always `workspace_member`.",
						},
					},
				},
			},
		},
	}
}

// --- Configure ---

func (d *WorkspaceMembersDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

// --- Read ---

func (d *WorkspaceMembersDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data WorkspaceMembersDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	workspaceID := data.WorkspaceID.ValueString()
	basePath := "/v1/organizations/workspaces/" + workspaceID + "/members"

	var allMembers []workspaceMembersListItem
	afterID := ""

	for {
		query := url.Values{}
		query.Set("limit", "1000")
		if afterID != "" {
			query.Set("after_id", afterID)
		}

		path := basePath + "?" + query.Encode()

		respBytes, err := d.adminClient.DoRequest(ctx, "GET", path, nil)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list workspace members: %s", err))
			return
		}

		var page workspaceMembersListResponse
		if err := json.Unmarshal(respBytes, &page); err != nil {
			resp.Diagnostics.AddError("Parse Error", fmt.Sprintf("Unable to parse workspace members response: %s", err))
			return
		}

		allMembers = append(allMembers, page.Data...)

		if !page.HasMore {
			break
		}
		afterID = page.LastID
	}

	memberObjs := make([]attr.Value, len(allMembers))
	for i, m := range allMembers {
		obj, diags := types.ObjectValue(workspaceMemberAttrTypes, map[string]attr.Value{
			"user_id":        types.StringValue(m.UserID),
			"workspace_id":   types.StringValue(m.WorkspaceID),
			"workspace_role": types.StringValue(m.WorkspaceRole),
			"type":           types.StringValue(m.Type),
		})
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		memberObjs[i] = obj
	}

	membersList, diags := types.ListValue(types.ObjectType{AttrTypes: workspaceMemberAttrTypes}, memberObjs)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.Members = membersList

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
