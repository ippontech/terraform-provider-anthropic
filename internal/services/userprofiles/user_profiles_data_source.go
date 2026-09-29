// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package userprofiles

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &UserProfilesDataSource{}

func NewUserProfilesDataSource() datasource.DataSource {
	return &UserProfilesDataSource{}
}

// UserProfilesDataSource defines the data source implementation.
type UserProfilesDataSource struct {
	client *anthropic.Client
}

// UserProfilesDataSourceModel describes the data source data model.
type UserProfilesDataSourceModel struct {
	Order        types.String `tfsdk:"order"`
	UserProfiles types.List   `tfsdk:"user_profiles"`
}

// userProfileDSListItemAttrTypes describes the attribute types of each
// element in the "user_profiles" list. It mirrors userProfileDSModel plus id.
var userProfileDSListItemAttrTypes = map[string]attr.Type{
	"id":           types.StringType,
	"access_type":  types.StringType,
	"external_id":  types.StringType,
	"name":         types.StringType,
	"metadata":     types.MapType{ElemType: types.StringType},
	"trust_grants": types.MapType{ElemType: userProfileTrustGrantObjectType},
	"type":         types.StringType,
	"created_at":   types.StringType,
	"updated_at":   types.StringType,
}

func (d *UserProfilesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user_profiles"
}

func (d *UserProfilesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists user profiles (beta). All pages are fetched automatically. This beta is not " +
			"generally available; the endpoint may return 404 for organizations that have not been enrolled.",
		Attributes: map[string]schema.Attribute{
			"order": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Sort order for results, by creation time. Either `asc` or `desc`.",
				Validators: []validator.String{
					stringvalidator.OneOf("asc", "desc"),
				},
			},
			"user_profiles": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "List of user profiles.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: userProfileDSAttributes(false),
				},
			},
		},
	}
}

func (d *UserProfilesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

	if !providerrors.RequireDataSourceAPIClient(pd.Client, &resp.Diagnostics) {
		return
	}

	d.client = pd.Client
}

// buildUserProfilesListParams translates the data source model into the SDK
// list params, forwarding "order" only when it is known and set.
func buildUserProfilesListParams(data UserProfilesDataSourceModel) anthropic.BetaUserProfileListParams {
	params := anthropic.BetaUserProfileListParams{}
	if !data.Order.IsNull() && !data.Order.IsUnknown() {
		params.Order = anthropic.BetaUserProfileListParamsOrder(data.Order.ValueString())
	}
	return params
}

func (d *UserProfilesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data UserProfilesDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	pager := d.client.Beta.UserProfiles.ListAutoPaging(ctx, buildUserProfilesListParams(data))
	profileObjs := make([]attr.Value, 0)
	for pager.Next() {
		profile := pager.Current()
		obj, diags := mapUserProfileDSToListObject(&profile)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		profileObjs = append(profileObjs, obj)
	}
	if err := pager.Err(); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list user profiles: %s", err))
		return
	}

	profilesList, diags := types.ListValue(types.ObjectType{AttrTypes: userProfileDSListItemAttrTypes}, profileObjs)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.UserProfiles = profilesList

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func mapUserProfileDSToListObject(profile *anthropic.BetaUserProfile) (attr.Value, diag.Diagnostics) {
	m, diags := mapUserProfileDSToModel(profile)
	if diags.HasError() {
		return nil, diags
	}

	obj, d := types.ObjectValue(userProfileDSListItemAttrTypes, map[string]attr.Value{
		"id":           m.ID,
		"access_type":  m.AccessType,
		"external_id":  m.ExternalID,
		"name":         m.Name,
		"metadata":     m.Metadata,
		"trust_grants": m.TrustGrants,
		"type":         m.Type,
		"created_at":   m.CreatedAt,
		"updated_at":   m.UpdatedAt,
	})
	diags.Append(d...)
	return obj, diags
}
