// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package rbac

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

var _ datasource.DataSource = &RBACGroupDataSource{}

func NewRBACGroupDataSource() datasource.DataSource {
	return &RBACGroupDataSource{}
}

// RBACGroupDataSource fetches one RBAC group by ID.
type RBACGroupDataSource struct {
	client *admin.Client
}

func (d *RBACGroupDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_rbac_group"
}

func (d *RBACGroupDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fetches an RBAC group of the Claude Enterprise organization by ID via the Admin API, with the IDs of the custom roles attached to it. " +
			"The key must be created for all linked organizations and carry the `read:rbac_groups` scope. " + enterpriseKeyNote,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "ID of the RBAC group (`rbac_group_...`).",
			},
			"name": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Name of the RBAC group. Not unique.",
			},
			"source_type": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "How the group was created: `direct` (in claude.ai or through the API) or `scim` (provisioned by the identity provider, read-only through the API).",
			},
			"role_ids": schema.SetAttribute{
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "IDs of the custom roles (`rbac_role_...`) attached to the group. Null when the API reported role data as temporarily unavailable.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the group was created.",
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the group was last updated.",
			},
		},
	}
}

func (d *RBACGroupDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *RBACGroupDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data rbacGroupModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	group, err := getJSON[rbacGroupAPIResponse](ctx, d.client, rbacGroupPath(data.ID.ValueString()))
	if err != nil {
		if admin.IsNotFound(err) {
			resp.Diagnostics.AddError("RBAC Group Not Found", fmt.Sprintf("No RBAC group with ID %q: %s", data.ID.ValueString(), err))
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read RBAC group: %s", err))
		return
	}

	model, diags := mapRBACGroup(group)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}
