// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package serviceaccounts

import (
	"context"
	"fmt"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &ServiceAccountDataSource{}

func NewServiceAccountDataSource() datasource.DataSource {
	return &ServiceAccountDataSource{}
}

// ServiceAccountDataSource defines the data source implementation.
type ServiceAccountDataSource struct {
	client *providerdata.OAuthClient
}

// ServiceAccountDataSourceModel describes the data source data model.
type ServiceAccountDataSourceModel struct {
	ID                types.String `tfsdk:"id"`
	Name              types.String `tfsdk:"name"`
	Description       types.String `tfsdk:"description"`
	OrganizationRole  types.String `tfsdk:"organization_role"`
	CreatedAt         types.String `tfsdk:"created_at"`
	UpdatedAt         types.String `tfsdk:"updated_at"`
	ArchivedAt        types.String `tfsdk:"archived_at"`
	CreatedByActorID  types.String `tfsdk:"created_by_actor_id"`
	UpdatedByActorID  types.String `tfsdk:"updated_by_actor_id"`
	ArchivedByActorID types.String `tfsdk:"archived_by_actor_id"`
}

func (d *ServiceAccountDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_service_account"
}

func (d *ServiceAccountDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fetches a single Workload Identity Federation (WIF) service account by ID (beta). A service account is a " +
			"named, non-human identity that federation rules target.\n\n" +
			"Requires an org:admin OAuth bearer token (`auth_token` / `ANTHROPIC_AUTH_TOKEN`); Admin API keys are not accepted on this " +
			"endpoint.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The service account identifier (`svac_...`).",
			},
			"name": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Admin-chosen slug identifier, unique within the organization.",
			},
			"description": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Optional free-text description, or null when unset.",
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
	}
}

func (d *ServiceAccountDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *ServiceAccountDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data ServiceAccountDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	sa, err := d.client.Beta.Organization.ServiceAccounts.Get(ctx, data.ID.ValueString(), anthropic.BetaOrganizationServiceAccountGetParams{})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read service account: %s", err))
		return
	}

	resp.Diagnostics.Append(mapServiceAccountDataSourceToState(sa, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// mapServiceAccountDataSourceToState maps the API response to the Terraform
// data source model. Named distinctly from the (sibling-branch) resource's
// mapServiceAccountToState to avoid a symbol collision once both land in the
// same package.
func mapServiceAccountDataSourceToState(sa *anthropic.BetaServiceAccount, data *ServiceAccountDataSourceModel) diag.Diagnostics {
	var diags diag.Diagnostics

	data.ID = types.StringValue(sa.ID)
	data.Name = types.StringValue(sa.Name)
	data.OrganizationRole = types.StringValue(string(sa.OrganizationRole))
	data.CreatedAt = types.StringValue(sa.CreatedAt.Format(time.RFC3339))
	data.UpdatedAt = types.StringValue(sa.UpdatedAt.Format(time.RFC3339))

	data.Description = serviceAccountDataSourceStringOrNull(sa.Description)
	data.CreatedByActorID = serviceAccountDataSourceStringOrNull(sa.CreatedByActorID)
	data.UpdatedByActorID = serviceAccountDataSourceStringOrNull(sa.UpdatedByActorID)
	data.ArchivedByActorID = serviceAccountDataSourceStringOrNull(sa.ArchivedByActorID)

	if sa.ArchivedAt.IsZero() {
		data.ArchivedAt = types.StringNull()
	} else {
		data.ArchivedAt = types.StringValue(sa.ArchivedAt.Format(time.RFC3339))
	}

	return diags
}

// serviceAccountDataSourceStringOrNull maps an API "" (Go zero value for a
// required-but-optional string field) to a null Terraform value, so an unset
// description or actor ID is represented as null rather than an empty string.
func serviceAccountDataSourceStringOrNull(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}
