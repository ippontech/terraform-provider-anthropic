// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &FederationIssuerDataSource{}

func NewFederationIssuerDataSource() datasource.DataSource {
	return &FederationIssuerDataSource{}
}

// FederationIssuerDataSource defines the data source implementation. It uses
// the OAuth bearer client, since the federation admin endpoints reject API
// keys outright (see providerdata.OAuthClient).
type FederationIssuerDataSource struct {
	client *providerdata.OAuthClient
}

// FederationIssuerDataSourceModel describes the data source data model.
//
// poll_status is exposed here (unlike the sibling anthropic_federation_issuer
// resource) because a data source re-reads on every plan by design, so it is
// always fresh.
type FederationIssuerDataSourceModel struct {
	ID                    types.String `tfsdk:"id"`
	Name                  types.String `tfsdk:"name"`
	IssuerURL             types.String `tfsdk:"issuer_url"`
	JWKS                  types.Object `tfsdk:"jwks"`
	CheckJTI              types.Bool   `tfsdk:"check_jti"`
	MaxJWTLifetimeSeconds types.Int64  `tfsdk:"max_jwt_lifetime_seconds"`
	JWKSPollingDisabledAt types.String `tfsdk:"jwks_polling_disabled_at"`
	PollStatus            types.Object `tfsdk:"poll_status"`
	CreatedAt             types.String `tfsdk:"created_at"`
	CreatedByActorID      types.String `tfsdk:"created_by_actor_id"`
	UpdatedAt             types.String `tfsdk:"updated_at"`
	UpdatedByActorID      types.String `tfsdk:"updated_by_actor_id"`
	ArchivedAt            types.String `tfsdk:"archived_at"`
	ArchivedByActorID     types.String `tfsdk:"archived_by_actor_id"`
}

// federationIssuerPollStatusAttrTypes describes the `poll_status` nested
// object. The `jwks` nested object reuses the resource's
// federationIssuerJWKSAttrTypes.
var federationIssuerPollStatusAttrTypes = map[string]attr.Type{
	"consecutive_failures": types.Int64Type,
	"last_fetched_at":      types.StringType,
	"next_poll_at":         types.StringType,
}

// federationIssuerDataSourceAttrTypes mirrors FederationIssuerDataSourceModel
// field for field. The anthropic_federation_issuers list data source uses it
// as its element type, building each entry from that model through
// types.ObjectValueFrom.
var federationIssuerDataSourceAttrTypes = map[string]attr.Type{
	"id":                       types.StringType,
	"name":                     types.StringType,
	"issuer_url":               types.StringType,
	"jwks":                     types.ObjectType{AttrTypes: federationIssuerJWKSAttrTypes},
	"check_jti":                types.BoolType,
	"max_jwt_lifetime_seconds": types.Int64Type,
	"jwks_polling_disabled_at": types.StringType,
	"poll_status":              types.ObjectType{AttrTypes: federationIssuerPollStatusAttrTypes},
	"created_at":               types.StringType,
	"created_by_actor_id":      types.StringType,
	"updated_at":               types.StringType,
	"updated_by_actor_id":      types.StringType,
	"archived_at":              types.StringType,
	"archived_by_actor_id":     types.StringType,
}

func (d *FederationIssuerDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_federation_issuer"
}

func (d *FederationIssuerDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fetches a Workload Identity Federation issuer by ID. " +
			"Requires an org:admin OAuth bearer token (`auth_token` / `ANTHROPIC_AUTH_TOKEN`); Admin API keys are not accepted on this endpoint.",
		Attributes: map[string]schema.Attribute{
			// --- Required ---
			"id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the federation issuer to retrieve (`fdis_...`).",
			},

			// --- Computed ---
			"name": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Admin-chosen slug identifier.",
			},
			"issuer_url": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The `iss` claim value that incoming JWTs must match exactly.",
			},
			"jwks": schema.SingleNestedAttribute{
				Computed:            true,
				MarkdownDescription: "How signing keys are obtained for signature verification.",
				Attributes: map[string]schema.Attribute{
					"type": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "JWKS mode: `discovery`, `explicit_url`, or `inline`.",
					},
					"discovery_base": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "Set when the OIDC discovery URL differs from `issuer_url`. Only populated for `discovery` mode.",
					},
					"url": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "Fixed JWKS endpoint. Only populated for `explicit_url` mode.",
					},
					"keys": schema.StringAttribute{
						Computed:            true,
						CustomType:          jsontypes.NormalizedType{},
						MarkdownDescription: "Inline JWK objects, as a JSON string. Only populated for `inline` mode.",
					},
					"ca_cert_pem": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "Custom CA (PEM) for TLS verification of the JWKS fetch. Only populated for `discovery` and `explicit_url` modes.",
					},
				},
			},
			"check_jti": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "Whether the jwt-bearer exchange enforces JTI single-use (replay protection) for tokens from this issuer.",
			},
			"max_jwt_lifetime_seconds": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Maximum allowed iat→exp spread for assertions from this issuer, in seconds.",
			},
			"jwks_polling_disabled_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp at which Anthropic's JWKS poller paused polling for this issuer after repeated fetch failures. Null while polling is active.",
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
						MarkdownDescription: "RFC 3339 timestamp of the last successful fetch. Null if never fetched.",
					},
					"next_poll_at": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "RFC 3339 timestamp of the next scheduled fetch. Null if paused.",
					},
				},
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Creation timestamp (RFC 3339).",
			},
			"created_by_actor_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Tagged ID (`user_`/`svac_`) of the actor that created this issuer.",
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Last update timestamp (RFC 3339).",
			},
			"updated_by_actor_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Tagged ID (`user_`/`svac_`) of the actor that last updated this issuer.",
			},
			"archived_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp at which this issuer was archived. Null if the issuer has not been archived.",
			},
			"archived_by_actor_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Tagged ID (`user_`/`svac_`) of the actor that archived this issuer. Null if the issuer has not been archived.",
			},
		},
	}
}

func (d *FederationIssuerDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *FederationIssuerDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data FederationIssuerDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	issuer, err := d.client.Beta.Organization.Federation.Issuers.Get(ctx, data.ID.ValueString(), anthropic.BetaOrganizationFederationIssuerGetParams{})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read federation issuer: %s", err))
		return
	}

	resp.Diagnostics.Append(mapFederationIssuerDataSourceToState(ctx, issuer, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// mapFederationIssuerDataSourceToState maps an API federation issuer response
// onto the data source model. Every attribute shared with the
// anthropic_federation_issuer resource is taken from mapFederationIssuerToState
// (jwks included), so the two views of an issuer agree on null handling and
// timestamp formatting; only poll_status, which the resource does not expose,
// is mapped here.
func mapFederationIssuerDataSourceToState(ctx context.Context, issuer *anthropic.BetaFederationIssuer, data *FederationIssuerDataSourceModel) diag.Diagnostics {
	var base FederationIssuerResourceModel
	diags := mapFederationIssuerToState(ctx, issuer, &base)
	if diags.HasError() {
		return diags
	}

	data.ID = base.ID
	data.Name = base.Name
	data.IssuerURL = base.IssuerURL
	data.JWKS = base.JWKS
	data.CheckJTI = base.CheckJTI
	data.MaxJWTLifetimeSeconds = base.MaxJWTLifetimeSeconds
	data.JWKSPollingDisabledAt = base.JWKSPollingDisabledAt
	data.CreatedAt = base.CreatedAt
	data.CreatedByActorID = base.CreatedByActorID
	data.UpdatedAt = base.UpdatedAt
	data.UpdatedByActorID = base.UpdatedByActorID
	data.ArchivedAt = base.ArchivedAt
	data.ArchivedByActorID = base.ArchivedByActorID

	pollStatusObj, d := mapFederationIssuerPollStatus(issuer.PollStatus)
	diags.Append(d...)
	data.PollStatus = pollStatusObj

	return diags
}

// mapFederationIssuerPollStatus maps the poll_status object. It is
// data-source-only: the resource omits poll_status entirely, since a resource
// only refreshes on plan/refresh cycles rather than the always-fresh read a
// data source performs.
func mapFederationIssuerPollStatus(status anthropic.BetaFederationIssuerPollStatus) (types.Object, diag.Diagnostics) {
	var diags diag.Diagnostics

	obj, d := types.ObjectValue(federationIssuerPollStatusAttrTypes, map[string]attr.Value{
		"consecutive_failures": types.Int64Value(status.ConsecutiveFailures),
		"last_fetched_at":      tfvalue.TimeOrNull(status.LastFetchedAt),
		"next_poll_at":         tfvalue.TimeOrNull(status.NextPollAt),
	})
	diags.Append(d...)
	return obj, diags
}
