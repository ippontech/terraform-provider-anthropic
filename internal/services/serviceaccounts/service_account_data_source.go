// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package serviceaccounts

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
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

// ServiceAccountDataSourceModel describes the data source data model. The
// data source exposes exactly the resource's attributes, so it shares the
// resource model and its mapServiceAccountToState mapping.
type ServiceAccountDataSourceModel = ServiceAccountResourceModel

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

	// data.Description is null here (only id comes from config), so the
	// resource mapper's "echo a configured empty description" rule never
	// fires: an API "" surfaces as null, as a data source should report it.
	resp.Diagnostics.Append(mapServiceAccountToState(sa, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
