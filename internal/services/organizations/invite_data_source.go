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

var _ datasource.DataSource = &InviteDataSource{}

func NewInviteDataSource() datasource.DataSource {
	return &InviteDataSource{}
}

type InviteDataSource struct {
	client *admin.Client
}

// InviteDataSourceModel maps the data source schema to Go types.
type InviteDataSourceModel struct {
	ID         types.String `tfsdk:"id"`
	Email      types.String `tfsdk:"email"`
	Role       types.String `tfsdk:"role"`
	Status     types.String `tfsdk:"status"`
	InvitedAt  types.String `tfsdk:"invited_at"`
	ExpiresAt  types.String `tfsdk:"expires_at"`
	AcceptedAt types.String `tfsdk:"accepted_at"`
	Type       types.String `tfsdk:"type"`
}

func (d *InviteDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_invite"
}

func (d *InviteDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fetches a single organization invite by ID via the Admin API. " +
			"Requires `admin_api_key` (or `ANTHROPIC_ADMIN_API_KEY`) to be configured on the provider.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Required:            true,
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
	}
}

func (d *InviteDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *InviteDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data InviteDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	respBytes, err := d.client.DoRequest(ctx, "GET", "/v1/organizations/invites/"+data.ID.ValueString()+"?beta=true", nil)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read invite: %s", err))
		return
	}

	var invite inviteAPIResponse
	if err := json.Unmarshal(respBytes, &invite); err != nil {
		resp.Diagnostics.AddError("Parse Error", fmt.Sprintf("Unable to parse invite response: %s", err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, mapInviteToDataSourceState(invite))...)
}

// mapInviteToDataSourceState copies the API Invite object into the Terraform
// data source state model. Named distinctly from invite_resource.go's
// mapInviteToState (same inviteAPIResponse input, but a different target
// model: InviteDataSourceModel here vs InviteResourceModel there).
func mapInviteToDataSourceState(invite inviteAPIResponse) InviteDataSourceModel {
	return InviteDataSourceModel{
		ID:         types.StringValue(invite.ID),
		Email:      types.StringValue(invite.Email),
		Role:       types.StringValue(invite.Role),
		Status:     types.StringValue(invite.Status),
		InvitedAt:  types.StringValue(invite.InvitedAt),
		ExpiresAt:  types.StringValue(invite.ExpiresAt),
		AcceptedAt: types.StringValue(invite.AcceptedAt),
		Type:       types.StringValue(invite.Type),
	}
}
