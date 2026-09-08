// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &FederationIssuersDataSource{}

func NewFederationIssuersDataSource() datasource.DataSource {
	return &FederationIssuersDataSource{}
}

// FederationIssuersDataSource defines the data source implementation.
//
// This endpoint rejects API keys outright and requires an org:admin OAuth
// bearer token, hence the OAuthClient wrapper rather than the plain
// *anthropic.Client used by standard-API data sources.
type FederationIssuersDataSource struct {
	client *providerdata.OAuthClient
}

// FederationIssuersDataSourceModel describes the data source data model.
type FederationIssuersDataSourceModel struct {
	IncludeArchived types.Bool `tfsdk:"include_archived"`
	Issuers         types.List `tfsdk:"issuers"`
}

func (d *FederationIssuersDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_federation_issuers"
}

func (d *FederationIssuersDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists Workload Identity Federation OIDC issuers registered in the organization (beta). All pages are fetched automatically.",
		Attributes: map[string]schema.Attribute{
			// --- Optional input ---
			"include_archived": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Whether to include archived issuers in the results. Defaults to `false`.",
			},

			// --- Computed output ---
			"issuers": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "List of federation issuers matching the filter.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Tagged ID of the federation issuer.",
						},
						"issuer_url": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "The `iss` claim value. Incoming JWTs must match exactly.",
						},
						"name": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Admin-chosen slug identifier.",
						},
						"check_jti": schema.BoolAttribute{
							Computed:            true,
							MarkdownDescription: "Whether the jwt-bearer exchange enforces JTI single-use (replay protection) for tokens from this issuer.",
						},
						"max_jwt_lifetime_seconds": schema.Int64Attribute{
							Computed:            true,
							MarkdownDescription: "Maximum allowed iat→exp spread for assertions from this issuer, in seconds.",
						},
						"jwks": schema.SingleNestedAttribute{
							Computed:            true,
							MarkdownDescription: "How signing keys are obtained for signature verification.",
							Attributes: map[string]schema.Attribute{
								"type": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "JWKS mode: `discovery`, `explicit_url`, or `inline`.",
								},
								"ca_cert_pem": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "Optional custom CA (PEM) for TLS verification of the JWKS fetch. Only set for `discovery` and `explicit_url` modes.",
								},
								"discovery_base": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "Set when the discovery URL differs from `issuer_url`. Only set for `discovery` mode.",
								},
								"url": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "JWKS endpoint. Only set for `explicit_url` mode.",
								},
								"keys": schema.StringAttribute{
									Computed:            true,
									CustomType:          jsontypes.NormalizedType{},
									MarkdownDescription: "Inline JWK objects, encoded as a JSON array string. Only set for `inline` mode.",
								},
							},
						},
						"jwks_polling_disabled_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "If set, Anthropic's JWKS poller has paused polling for this issuer after repeated fetch failures (RFC 3339). Null while polling is active.",
						},
						"poll_status": schema.SingleNestedAttribute{
							Computed:            true,
							MarkdownDescription: "Status of automatic JWKS polling for this issuer.",
							Attributes: map[string]schema.Attribute{
								"consecutive_failures": schema.Int64Attribute{
									Computed:            true,
									MarkdownDescription: "Consecutive fetch failures since the last success.",
								},
								"last_fetched_at": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "When the last successful fetch completed (RFC 3339). Null if never fetched.",
								},
								"next_poll_at": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "When the next fetch is scheduled (RFC 3339). Null if paused.",
								},
							},
						},
						"created_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "When this issuer was created (RFC 3339).",
						},
						"created_by_actor_id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Tagged ID (`user_`/`svac_`) of the actor that created this issuer.",
						},
						"updated_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "When this issuer was last updated (RFC 3339).",
						},
						"updated_by_actor_id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Tagged ID (`user_`/`svac_`) of the actor that last updated this issuer.",
						},
						"archived_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "When this issuer was archived (RFC 3339). Null if the issuer has not been archived.",
						},
						"archived_by_actor_id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Tagged ID (`user_`/`svac_`) of the actor that archived this issuer. Null if the issuer has not been archived.",
						},
					},
				},
			},
		},
	}
}

func (d *FederationIssuersDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *FederationIssuersDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data FederationIssuersDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	params := anthropic.BetaOrganizationFederationIssuerListParams{}
	if !data.IncludeArchived.IsNull() && !data.IncludeArchived.IsUnknown() {
		params.IncludeArchived = param.NewOpt(data.IncludeArchived.ValueBool())
	}

	pager := d.client.Beta.Organization.Federation.Issuers.ListAutoPaging(ctx, params)

	issuerObjs := make([]attr.Value, 0)
	for pager.Next() {
		issuer := pager.Current()
		obj, diags := mapFederationIssuersListEntry(ctx, &issuer)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		issuerObjs = append(issuerObjs, obj)
	}

	if err := pager.Err(); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list federation issuers: %s", err))
		return
	}

	issuersList, diags := types.ListValue(types.ObjectType{AttrTypes: federationIssuerDataSourceAttrTypes}, issuerObjs)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.Issuers = issuersList

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// mapFederationIssuersListEntry converts an API federation issuer response to
// a Terraform object value for inclusion in the "issuers" list. Each entry is
// the anthropic_federation_issuer data source model, so it is produced by that
// data source's mapping rather than mapped a third time.
func mapFederationIssuersListEntry(ctx context.Context, issuer *anthropic.BetaFederationIssuer) (attr.Value, diag.Diagnostics) {
	var model FederationIssuerDataSourceModel
	diags := mapFederationIssuerDataSourceToState(ctx, issuer, &model)
	if diags.HasError() {
		return types.ObjectNull(federationIssuerDataSourceAttrTypes), diags
	}

	obj, d := types.ObjectValueFrom(ctx, federationIssuerDataSourceAttrTypes, model)
	diags.Append(d...)
	return obj, diags
}
