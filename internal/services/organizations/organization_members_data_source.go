// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package organizations

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

var _ datasource.DataSource = &OrganizationMembersDataSource{}

func NewOrganizationMembersDataSource() datasource.DataSource {
	return &OrganizationMembersDataSource{}
}

type OrganizationMembersDataSource struct {
	client *admin.Client
}

// OrganizationMembersDataSourceModel describes the data source data model.
type OrganizationMembersDataSourceModel struct {
	Email   types.String `tfsdk:"email"`
	Members types.List   `tfsdk:"members"`
}

var organizationMemberAttrTypes = map[string]attr.Type{
	"id":       types.StringType,
	"email":    types.StringType,
	"name":     types.StringType,
	"role":     types.StringType,
	"added_at": types.StringType,
	"type":     types.StringType,
}

// organizationMembersListResponse mirrors the paginated list envelope.
type organizationMembersListResponse struct {
	Data    []organizationMemberAPIResponse `json:"data"`
	HasMore bool                            `json:"has_more"`
	FirstID string                          `json:"first_id"`
	LastID  string                          `json:"last_id"`
}

func (d *OrganizationMembersDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization_members"
}

func (d *OrganizationMembersDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists all members (Users) of the organization via the Admin API. All pages are fetched automatically. " +
			"Results can optionally be filtered by `email`. " +
			"Requires `admin_api_key` (or `ANTHROPIC_ADMIN_API_KEY`) to be configured on the provider.",
		Attributes: map[string]schema.Attribute{
			"email": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Filter by user email. Returns only members whose email matches.",
			},
			"members": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "List of organization members matching the specified filter.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Computed:            true,
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
				},
			},
		},
	}
}

func (d *OrganizationMembersDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *OrganizationMembersDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data OrganizationMembersDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var allMembers []organizationMemberAPIResponse
	afterID := ""

	for {
		params := url.Values{}
		params.Set("limit", "1000")
		if afterID != "" {
			params.Set("after_id", afterID)
		}
		if !data.Email.IsNull() && !data.Email.IsUnknown() {
			params.Set("email", data.Email.ValueString())
		}

		apiPath := "/v1/organizations/users?" + params.Encode()

		respBytes, err := d.client.DoRequest(ctx, "GET", apiPath, nil)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list organization members: %s", err))
			return
		}

		var page organizationMembersListResponse
		if err := json.Unmarshal(respBytes, &page); err != nil {
			resp.Diagnostics.AddError("Parse Error", fmt.Sprintf("Unable to parse organization members response: %s", err))
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
		obj, diags := types.ObjectValue(organizationMemberAttrTypes, map[string]attr.Value{
			"id":       types.StringValue(m.ID),
			"email":    types.StringValue(m.Email),
			"name":     types.StringValue(m.Name),
			"role":     types.StringValue(m.Role),
			"added_at": types.StringValue(m.AddedAt),
			"type":     types.StringValue(m.Type),
		})
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		memberObjs[i] = obj
	}

	membersList, diags := types.ListValue(types.ObjectType{AttrTypes: organizationMemberAttrTypes}, memberObjs)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.Members = membersList

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
