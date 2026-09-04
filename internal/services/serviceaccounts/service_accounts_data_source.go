// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package serviceaccounts

import (
	"context"
	"fmt"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &ServiceAccountsDataSource{}

func NewServiceAccountsDataSource() datasource.DataSource {
	return &ServiceAccountsDataSource{}
}

// ServiceAccountsDataSource defines the data source implementation.
type ServiceAccountsDataSource struct {
	client *providerdata.OAuthClient
}

// ServiceAccountsDataSourceModel describes the data source data model.
type ServiceAccountsDataSourceModel struct {
	IncludeArchived types.Bool `tfsdk:"include_archived"`
	ServiceAccounts types.List `tfsdk:"service_accounts"`
}

// serviceAccountsListAttrTypes describes the attribute types of each element in
// the "service_accounts" list. It mirrors the anthropic_service_account
// resource/data source schema. Named distinctly from any sibling-branch
// "serviceAccountAttrTypes" (singular) to avoid a duplicate declaration once
// both land in the same package.
var serviceAccountsListAttrTypes = map[string]attr.Type{
	"id":                   types.StringType,
	"name":                 types.StringType,
	"description":          types.StringType,
	"organization_role":    types.StringType,
	"created_at":           types.StringType,
	"updated_at":           types.StringType,
	"archived_at":          types.StringType,
	"created_by_actor_id":  types.StringType,
	"updated_by_actor_id":  types.StringType,
	"archived_by_actor_id": types.StringType,
}

func (d *ServiceAccountsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_service_accounts"
}

func (d *ServiceAccountsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists Workload Identity Federation (WIF) service accounts in the caller's organization (beta). All pages " +
			"are fetched automatically. Requires an org:admin OAuth bearer token (`auth_token` / `ANTHROPIC_AUTH_TOKEN`); Admin API keys " +
			"are not accepted on this endpoint.",
		Attributes: map[string]schema.Attribute{
			// --- Optional inputs ---
			"include_archived": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Whether to include archived service accounts in the results. Defaults to `false`.",
			},

			// --- Computed output ---
			"service_accounts": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "List of service accounts in the organization.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Unique service account identifier assigned by the API (`svac_...`).",
						},
						"name": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Admin-chosen slug identifier, unique within the organization.",
						},
						"description": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Optional free-text description.",
						},
						"organization_role": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Org-level role: `developer` or `admin`.",
						},
						"created_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "RFC 3339 timestamp of when the service account was created.",
						},
						"updated_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "RFC 3339 timestamp of when the service account was last updated.",
						},
						"archived_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "RFC 3339 timestamp of when the service account was archived, or null while it is live.",
						},
						"created_by_actor_id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Tagged ID (`user_...`/`svac_...`) of the actor that created this service account.",
						},
						"updated_by_actor_id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Tagged ID (`user_...`/`svac_...`) of the actor that last updated this service account.",
						},
						"archived_by_actor_id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Tagged ID (`user_...`/`svac_...`) of the actor that archived this service account, or null while it is live.",
						},
					},
				},
			},
		},
	}
}

func (d *ServiceAccountsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *ServiceAccountsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data ServiceAccountsDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	params := anthropic.BetaOrganizationServiceAccountListParams{}
	if !data.IncludeArchived.IsNull() && !data.IncludeArchived.IsUnknown() {
		params.IncludeArchived = param.NewOpt(data.IncludeArchived.ValueBool())
	}

	pager := d.client.Beta.Organization.ServiceAccounts.ListAutoPaging(ctx, params)

	objs := make([]attr.Value, 0)
	for pager.Next() {
		sa := pager.Current()
		obj, diags := mapServiceAccountsListEntry(&sa)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		objs = append(objs, obj)
	}

	if err := pager.Err(); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list service accounts: %s", err))
		return
	}

	list, diags := types.ListValue(types.ObjectType{AttrTypes: serviceAccountsListAttrTypes}, objs)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.ServiceAccounts = list

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// mapServiceAccountsListEntry converts an API service account into a Terraform
// object value for inclusion in the "service_accounts" list. Named distinctly
// from any sibling-branch "mapServiceAccountToState" to avoid a duplicate
// declaration once both land in the same package.
func mapServiceAccountsListEntry(sa *anthropic.BetaServiceAccount) (attr.Value, diag.Diagnostics) {
	archivedAt := types.StringNull()
	if !sa.ArchivedAt.IsZero() {
		archivedAt = types.StringValue(sa.ArchivedAt.Format(time.RFC3339))
	}

	return types.ObjectValue(serviceAccountsListAttrTypes, map[string]attr.Value{
		"id":                   types.StringValue(sa.ID),
		"name":                 types.StringValue(sa.Name),
		"description":          serviceAccountsStringOrNull(sa.Description),
		"organization_role":    types.StringValue(string(sa.OrganizationRole)),
		"created_at":           types.StringValue(sa.CreatedAt.Format(time.RFC3339)),
		"updated_at":           types.StringValue(sa.UpdatedAt.Format(time.RFC3339)),
		"archived_at":          archivedAt,
		"created_by_actor_id":  serviceAccountsStringOrNull(sa.CreatedByActorID),
		"updated_by_actor_id":  serviceAccountsStringOrNull(sa.UpdatedByActorID),
		"archived_by_actor_id": serviceAccountsStringOrNull(sa.ArchivedByActorID),
	})
}

// serviceAccountsStringOrNull maps an API "" (Go zero value for a
// required-but-optional string field) to a null Terraform value. Named
// distinctly from any sibling-branch "stringOrNull" to avoid a duplicate
// declaration once both land in the same package.
func serviceAccountsStringOrNull(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}
