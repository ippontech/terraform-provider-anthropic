// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package userprofiles

import (
	"context"
	"fmt"
	"time"

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
var _ datasource.DataSource = &UserProfileDataSource{}

func NewUserProfileDataSource() datasource.DataSource {
	return &UserProfileDataSource{}
}

// UserProfileDataSource defines the data source implementation.
type UserProfileDataSource struct {
	client *anthropic.Client
}

// userProfileDSTrustGrantAttrTypes describes the attribute types of each
// element in the "trust_grants" map.
var userProfileDSTrustGrantAttrTypes = map[string]attr.Type{
	"status": types.StringType,
}

// userProfileDSModel describes the fields shared by the singular data source
// and each element of the plural data source's "user_profiles" list.
type userProfileDSModel struct {
	ID          types.String `tfsdk:"id"`
	AccessType  types.String `tfsdk:"access_type"`
	ExternalID  types.String `tfsdk:"external_id"`
	Name        types.String `tfsdk:"name"`
	Metadata    types.Map    `tfsdk:"metadata"`
	TrustGrants types.Map    `tfsdk:"trust_grants"`
	Type        types.String `tfsdk:"type"`
	CreatedAt   types.String `tfsdk:"created_at"`
	UpdatedAt   types.String `tfsdk:"updated_at"`
}

// userProfileDSAttributes returns the schema attributes shared by the
// singular data source and the nested object of the plural data source's
// list. includeID controls whether the "id" attribute is Required (singular)
// or Computed (nested list element).
func userProfileDSAttributes(idRequired bool) map[string]schema.Attribute {
	idAttr := schema.StringAttribute{
		MarkdownDescription: "Unique identifier for the user profile (e.g. `uprof_...`).",
	}
	if idRequired {
		idAttr.Required = true
	} else {
		idAttr.Computed = true
	}

	return map[string]schema.Attribute{
		"id": idAttr,
		"access_type": schema.StringAttribute{
			Computed: true,
			MarkdownDescription: "How the platform uses the API on behalf of the entity this profile represents. " +
				"`application`: the platform sells a product that uses the API behind the scenes, and the profile " +
				"represents an individual end-user of that product. `passthrough`: the platform resells raw inference, " +
				"and the profile identifies the resold-to company.",
		},
		"external_id": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "Platform's own identifier for this user. Not enforced unique. Null if not set.",
		},
		"name": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "Real-world name of the entity this profile represents (company or individual). Null if not set.",
		},
		"metadata": schema.MapAttribute{
			Computed:            true,
			ElementType:         types.StringType,
			MarkdownDescription: "Arbitrary key-value metadata attached to the profile.",
		},
		"trust_grants": schema.MapAttribute{
			Computed:    true,
			ElementType: types.ObjectType{AttrTypes: userProfileDSTrustGrantAttrTypes},
			MarkdownDescription: "Trust grants for this profile, keyed by grant name. A grant name is absent from the " +
				"map when it has no active or in-flight grant. Each entry has a `status` (`active`, `pending`, or `rejected`).",
		},
		"type": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "Object type. Always `user_profile`.",
		},
		"created_at": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "Creation timestamp (RFC 3339).",
		},
		"updated_at": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "Last update timestamp (RFC 3339).",
		},
	}
}

func (d *UserProfileDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user_profile"
}

func (d *UserProfileDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fetches a single user profile by ID (beta). This beta is not generally available; " +
			"the endpoint may return 404 for organizations that have not been enrolled.",
		Attributes: userProfileDSAttributes(true),
	}
}

func (d *UserProfileDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *UserProfileDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data userProfileDSModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	profile, err := d.client.Beta.UserProfiles.Get(ctx, data.ID.ValueString(), anthropic.BetaUserProfileGetParams{})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to retrieve user profile: %s", err))
		return
	}

	model, diags := mapUserProfileDSToModel(profile)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, model)...)
}

// mapUserProfileDSToModel maps the API response into userProfileDSModel,
// shared by the singular data source and each element of the plural data
// source's "user_profiles" list.
func mapUserProfileDSToModel(profile *anthropic.BetaUserProfile) (userProfileDSModel, diag.Diagnostics) {
	var diags diag.Diagnostics

	m := userProfileDSModel{
		ID:         types.StringValue(profile.ID),
		AccessType: types.StringValue(string(profile.AccessType)),
		Type:       types.StringValue(string(profile.Type)),
		CreatedAt:  types.StringValue(profile.CreatedAt.Format(time.RFC3339)),
		UpdatedAt:  types.StringValue(profile.UpdatedAt.Format(time.RFC3339)),
	}

	if profile.ExternalID == "" {
		m.ExternalID = types.StringNull()
	} else {
		m.ExternalID = types.StringValue(profile.ExternalID)
	}

	if profile.Name == "" {
		m.Name = types.StringNull()
	} else {
		m.Name = types.StringValue(profile.Name)
	}

	if len(profile.Metadata) > 0 {
		elements := make(map[string]attr.Value, len(profile.Metadata))
		for k, v := range profile.Metadata {
			elements[k] = types.StringValue(v)
		}
		metaMap, d := types.MapValue(types.StringType, elements)
		diags.Append(d...)
		m.Metadata = metaMap
	} else {
		m.Metadata = types.MapNull(types.StringType)
	}

	if len(profile.TrustGrants) > 0 {
		elements := make(map[string]attr.Value, len(profile.TrustGrants))
		for k, v := range profile.TrustGrants {
			obj, d := types.ObjectValue(userProfileDSTrustGrantAttrTypes, map[string]attr.Value{
				"status": types.StringValue(string(v.Status)),
			})
			diags.Append(d...)
			elements[k] = obj
		}
		grantsMap, d := types.MapValue(types.ObjectType{AttrTypes: userProfileDSTrustGrantAttrTypes}, elements)
		diags.Append(d...)
		m.TrustGrants = grantsMap
	} else {
		m.TrustGrants = types.MapNull(types.ObjectType{AttrTypes: userProfileDSTrustGrantAttrTypes})
	}

	return m, diags
}
