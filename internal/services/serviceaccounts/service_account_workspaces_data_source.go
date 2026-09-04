// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package serviceaccounts

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &ServiceAccountWorkspacesDataSource{}

func NewServiceAccountWorkspacesDataSource() datasource.DataSource {
	return &ServiceAccountWorkspacesDataSource{}
}

// ServiceAccountWorkspacesDataSource defines the data source implementation.
// It uses the OAuth bearer client: this endpoint rejects API keys outright.
type ServiceAccountWorkspacesDataSource struct {
	client *providerdata.OAuthClient
}

// ServiceAccountWorkspacesDataSourceModel describes the data source data model.
type ServiceAccountWorkspacesDataSourceModel struct {
	ServiceAccountID types.String `tfsdk:"service_account_id"`
	Workspaces       types.List   `tfsdk:"workspaces"`
}

// serviceAccountWorkspacesListItemAttrTypes describes the attribute types of
// each element in the "workspaces" list.
var serviceAccountWorkspacesListItemAttrTypes = map[string]attr.Type{
	"workspace_id":        types.StringType,
	"workspace_role":      types.StringType,
	"implicit":            types.BoolType,
	"created_by_actor_id": types.StringType,
}

// --- Schema ---

func (d *ServiceAccountWorkspacesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_service_account_workspaces"
}

func (d *ServiceAccountWorkspacesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the workspace memberships (implicit and explicit) of a Workload Identity Federation service account. " +
			"All pages are fetched automatically. Requires an org:admin OAuth bearer token (`auth_token` / `ANTHROPIC_AUTH_TOKEN`); Admin API keys are not accepted.",
		Attributes: map[string]schema.Attribute{
			"service_account_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the service account whose workspace memberships to list.",
			},
			"workspaces": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "List of workspace memberships for the service account.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"workspace_id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Tagged workspace ID (`wrkspc_...`).",
						},
						"workspace_role": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Role of the service account in this workspace (e.g. `workspace_admin`, `workspace_developer`, `workspace_restricted_developer`, `workspace_user`). Service accounts cannot hold the `workspace_billing` role.",
						},
						"implicit": schema.BoolAttribute{
							Computed:            true,
							MarkdownDescription: "True when this is the implicit default-workspace membership every service account has when no explicit membership exists. Implicit memberships have role `workspace_user` and cannot be removed.",
						},
						"created_by_actor_id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Tagged ID (`user_...`/`svac_...`) of the actor who created this membership.",
						},
					},
				},
			},
		},
	}
}

// --- Configure ---

func (d *ServiceAccountWorkspacesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

	if !providerrors.RequireOAuthDataSourceClient(pd.OAuthClient, &resp.Diagnostics) {
		return
	}

	d.client = pd.OAuthClient
}

// --- Read ---

func (d *ServiceAccountWorkspacesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data ServiceAccountWorkspacesDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	serviceAccountID := data.ServiceAccountID.ValueString()

	pager := d.client.Beta.Organization.ServiceAccounts.Workspaces.ListAutoPaging(ctx, serviceAccountID, anthropic.BetaOrganizationServiceAccountWorkspaceListParams{})

	workspaceObjs := make([]attr.Value, 0)
	for pager.Next() {
		member := pager.Current()
		obj, diags := mapServiceAccountWorkspacesListEntry(&member)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		workspaceObjs = append(workspaceObjs, obj)
	}

	if err := pager.Err(); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list service account workspaces: %s", err))
		return
	}

	workspacesList, diags := types.ListValue(types.ObjectType{AttrTypes: serviceAccountWorkspacesListItemAttrTypes}, workspaceObjs)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.Workspaces = workspacesList

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// mapServiceAccountWorkspacesListEntry converts an API service account
// workspace membership into a Terraform object value for inclusion in the
// "workspaces" list.
func mapServiceAccountWorkspacesListEntry(member *anthropic.BetaServiceAccountWorkspaceMember) (attr.Value, diag.Diagnostics) {
	// The API omits the creating actor on implicit (default-workspace)
	// memberships; an empty string must surface as null, not "".
	createdByActorID := types.StringNull()
	if member.CreatedByActorID != "" {
		createdByActorID = types.StringValue(member.CreatedByActorID)
	}
	obj, diags := types.ObjectValue(serviceAccountWorkspacesListItemAttrTypes, map[string]attr.Value{
		"workspace_id":        types.StringValue(member.WorkspaceID),
		"workspace_role":      types.StringValue(string(member.WorkspaceRole)),
		"implicit":            types.BoolValue(member.Implicit),
		"created_by_actor_id": createdByActorID,
	})
	return obj, diags
}
