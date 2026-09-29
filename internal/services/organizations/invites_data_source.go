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

var _ datasource.DataSource = &InvitesDataSource{}

func NewInvitesDataSource() datasource.DataSource {
	return &InvitesDataSource{}
}

type InvitesDataSource struct {
	client *admin.Client
}

// InvitesDataSourceModel describes the data source data model.
type InvitesDataSourceModel struct {
	Email    types.String `tfsdk:"email"`
	Statuses types.List   `tfsdk:"statuses"`
	Invites  types.List   `tfsdk:"invites"`
}

var inviteAttrTypes = map[string]attr.Type{
	"id":          types.StringType,
	"email":       types.StringType,
	"role":        types.StringType,
	"status":      types.StringType,
	"invited_at":  types.StringType,
	"expires_at":  types.StringType,
	"accepted_at": types.StringType,
	"type":        types.StringType,
}

// invitesListResponse mirrors the paginated list envelope.
type invitesListResponse struct {
	Data    []inviteAPIResponse `json:"data"`
	HasMore bool                `json:"has_more"`
	FirstID string              `json:"first_id"`
	LastID  string              `json:"last_id"`
}

func (d *InvitesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_invites"
}

func (d *InvitesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists all organization invites via the Admin API. All pages are fetched automatically. " +
			"Results can optionally be filtered by `email` and/or `statuses`. " +
			"Requires `admin_api_key` (or `ANTHROPIC_ADMIN_API_KEY`) to be configured on the provider.",
		Attributes: map[string]schema.Attribute{
			"email": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Filter by the email address the invite was sent to (exact match).",
			},
			"statuses": schema.ListAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Filter to invites whose status is one of the supplied values (`pending`, `accepted`, `expired`, `deleted`).",
			},
			"invites": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "List of invites matching the specified filters.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "The ID of the invite.",
						},
						"email": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Email of the user being invited.",
						},
						"role": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Organization role the user will hold once the invite is accepted.",
						},
						"status": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Status of the invite. One of `pending`, `accepted`, `expired`, `deleted`.",
						},
						"invited_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "RFC 3339 timestamp of when the invite was created.",
						},
						"expires_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "RFC 3339 timestamp of when the invite expires.",
						},
						"accepted_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "RFC 3339 timestamp of when the invite was accepted. Empty if not yet accepted.",
						},
						"type": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Object type. Always `invite`.",
						},
					},
				},
			},
		},
	}
}

func (d *InvitesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

// buildInvitesQuery builds the query parameters for one page of the list-invites
// request: the fixed page limit, the required `beta=true` flag, an optional
// pagination cursor, an optional email filter, and optional status filters.
// afterID and email are omitted when empty; statuses are repeated as `statuses`
// (OR'ed by the API) and omitted when empty.
func buildInvitesQuery(afterID, email string, statuses []string) url.Values {
	params := url.Values{}
	params.Set("beta", "true")
	params.Set("limit", "1000")
	if afterID != "" {
		params.Set("after_id", afterID)
	}
	if email != "" {
		params.Set("email", email)
	}
	for _, status := range statuses {
		params.Add("statuses", status)
	}
	return params
}

func (d *InvitesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data InvitesDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	email := ""
	if !data.Email.IsNull() && !data.Email.IsUnknown() {
		email = data.Email.ValueString()
	}

	var statuses []string
	if !data.Statuses.IsNull() && !data.Statuses.IsUnknown() {
		resp.Diagnostics.Append(data.Statuses.ElementsAs(ctx, &statuses, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	var allInvites []inviteAPIResponse
	afterID := ""

	for {
		apiPath := "/v1/organizations/invites?" + buildInvitesQuery(afterID, email, statuses).Encode()

		respBytes, err := d.client.DoRequest(ctx, "GET", apiPath, nil)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list invites: %s", err))
			return
		}

		var page invitesListResponse
		if err := json.Unmarshal(respBytes, &page); err != nil {
			resp.Diagnostics.AddError("Parse Error", fmt.Sprintf("Unable to parse invites response: %s", err))
			return
		}

		allInvites = append(allInvites, page.Data...)

		if !page.HasMore {
			break
		}
		afterID = page.LastID
	}

	inviteObjs := make([]attr.Value, len(allInvites))
	for i, invite := range allInvites {
		obj, diags := types.ObjectValue(inviteAttrTypes, map[string]attr.Value{
			"id":          types.StringValue(invite.ID),
			"email":       types.StringValue(invite.Email),
			"role":        types.StringValue(invite.Role),
			"status":      types.StringValue(invite.Status),
			"invited_at":  types.StringValue(invite.InvitedAt),
			"expires_at":  types.StringValue(invite.ExpiresAt),
			"accepted_at": types.StringValue(invite.AcceptedAt),
			"type":        types.StringValue(invite.Type),
		})
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		inviteObjs[i] = obj
	}

	invitesList, diags := types.ListValue(types.ObjectType{AttrTypes: inviteAttrTypes}, inviteObjs)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.Invites = invitesList

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
