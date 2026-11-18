// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package rbac

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

var _ datasource.DataSource = &RBACGroupsDataSource{}

func NewRBACGroupsDataSource() datasource.DataSource {
	return &RBACGroupsDataSource{}
}

// RBACGroupsDataSource lists every RBAC group of the enterprise.
type RBACGroupsDataSource struct {
	client *admin.Client
}

// RBACGroupsDataSourceModel describes the data source data model.
type RBACGroupsDataSourceModel struct {
	RBACGroups types.List `tfsdk:"rbac_groups"`
}

func (d *RBACGroupsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_rbac_groups"
}

func (d *RBACGroupsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists every RBAC group of the Claude Enterprise organization via the Admin API, identity-provider (`scim`) groups included. " +
			"All pages are fetched automatically. The key must be created for all linked organizations and carry the `read:rbac_groups` scope. " + enterpriseKeyNote,
		Attributes: map[string]schema.Attribute{
			"rbac_groups": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "The RBAC groups, in API order. Empty (never null) when there are none.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "ID of the RBAC group (`rbac_group_...`).",
						},
						"name": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Name of the RBAC group. Not unique.",
						},
						"source_type": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "How the group was created: `direct` or `scim`.",
						},
						"role_ids": schema.SetAttribute{
							Computed:            true,
							ElementType:         types.StringType,
							MarkdownDescription: "IDs of the custom roles attached to the group. Null when the API reported role data as temporarily unavailable.",
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
				},
			},
		},
	}
}

func (d *RBACGroupsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *RBACGroupsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	groups, err := listAll[rbacGroupAPIResponse](ctx, d.client, rbacGroupsPath)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list RBAC groups: %s", err))
		return
	}

	values := make([]attr.Value, 0, len(groups))
	for _, g := range groups {
		model, diags := mapRBACGroup(g)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		obj, diags := types.ObjectValue(rbacGroupAttrTypes, model.attrValues())
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		values = append(values, obj)
	}

	list, diags := types.ListValue(types.ObjectType{AttrTypes: rbacGroupAttrTypes}, values)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &RBACGroupsDataSourceModel{RBACGroups: list})...)
}
