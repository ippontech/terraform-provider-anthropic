// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package organizations

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

var _ datasource.DataSource = &OrganizationMemberDataSource{}

func NewOrganizationMemberDataSource() datasource.DataSource {
	return &OrganizationMemberDataSource{}
}

type OrganizationMemberDataSource struct {
	client *admin.Client
}

// OrganizationMemberDataSourceModel maps the data source schema to Go types.
type OrganizationMemberDataSourceModel struct {
	ID      types.String `tfsdk:"id"`
	Email   types.String `tfsdk:"email"`
	Name    types.String `tfsdk:"name"`
	Role    types.String `tfsdk:"role"`
	AddedAt types.String `tfsdk:"added_at"`
	Type    types.String `tfsdk:"type"`
}

// organizationMemberAPIResponse mirrors the User object returned by the Admin API.
type organizationMemberAPIResponse struct {
	ID      string `json:"id"`
	Email   string `json:"email"`
	Name    string `json:"name"`
	Role    string `json:"role"`
	AddedAt string `json:"added_at"`
	Type    string `json:"type"`
}

func (d *OrganizationMemberDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization_member"
}

func (d *OrganizationMemberDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fetches a single member (User) of the organization by user ID via the Admin API. " +
			"Useful for resolving a user's email, name, or organization role from their ID. " +
			"Requires `admin_api_key` (or `ANTHROPIC_ADMIN_API_KEY`) to be configured on the provider.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the user.",
			},
			"email": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Email of the user.",
			},
			"name": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Name of the user.",
			},
			"role": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Organization role of the user (e.g. `user`, `developer`, `billing`, `admin`, `claude_code_user`).",
			},
			"added_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the user joined the organization.",
			},
			"type": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Object type. Always `user`.",
			},
		},
	}
}

func (d *OrganizationMemberDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

	d.client = pd.AdminClient
}

func (d *OrganizationMemberDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data OrganizationMemberDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	respBytes, err := d.client.DoRequest(ctx, "GET", "/v1/organizations/users/"+data.ID.ValueString(), nil)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read organization member: %s", err))
		return
	}

	var member organizationMemberAPIResponse
	if err := json.Unmarshal(respBytes, &member); err != nil {
		resp.Diagnostics.AddError("Parse Error", fmt.Sprintf("Unable to parse organization member response: %s", err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, mapOrganizationMemberToState(member))...)
}

// mapOrganizationMemberToState copies the API User object into the Terraform state model.
func mapOrganizationMemberToState(member organizationMemberAPIResponse) OrganizationMemberDataSourceModel {
	return OrganizationMemberDataSourceModel{
		ID:      types.StringValue(member.ID),
		Email:   types.StringValue(member.Email),
		Name:    types.StringValue(member.Name),
		Role:    types.StringValue(member.Role),
		AddedAt: types.StringValue(member.AddedAt),
		Type:    types.StringValue(member.Type),
	}
}
