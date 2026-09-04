// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation

import (
	"context"
	"fmt"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
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

// federationIssuerDataSourceJWKSAttrTypes describes the `jwks` nested object.
// Named uniquely (suffixed with the data source name) rather than a generic
// jwksAttrTypes: the sibling anthropic_federation_issuer resource, developed
// on a different branch in the same eventual package, defines its own copy,
// and this avoids a duplicate-symbol compile break once both branches merge.
var federationIssuerDataSourceJWKSAttrTypes = map[string]attr.Type{
	"type":           types.StringType,
	"discovery_base": types.StringType,
	"url":            types.StringType,
	"keys":           jsontypes.NormalizedType{},
	"ca_cert_pem":    types.StringType,
}

// federationIssuerDataSourcePollStatusAttrTypes describes the `poll_status`
// nested object. See federationIssuerDataSourceJWKSAttrTypes for the naming
// rationale.
var federationIssuerDataSourcePollStatusAttrTypes = map[string]attr.Type{
	"consecutive_failures": types.Int64Type,
	"last_fetched_at":      types.StringType,
	"next_poll_at":         types.StringType,
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

	resp.Diagnostics.Append(mapFederationIssuerDataSourceToState(issuer, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// mapFederationIssuerDataSourceToState maps an API federation issuer response
// onto the data source model. Named uniquely (suffixed with the data source
// name) rather than the generic mapFederationIssuerToState the spec
// describes sharing with the resource: that resource lives on a sibling
// branch with no code on this one, so a shared helper cannot exist yet
// without inventing symbols that don't compile here. Deduping is deferred
// until both land on main.
func mapFederationIssuerDataSourceToState(issuer *anthropic.BetaFederationIssuer, data *FederationIssuerDataSourceModel) diag.Diagnostics {
	var diags diag.Diagnostics

	data.ID = types.StringValue(issuer.ID)
	data.Name = types.StringValue(issuer.Name)
	data.IssuerURL = types.StringValue(issuer.IssuerURL)
	data.CheckJTI = types.BoolValue(issuer.CheckJTI)
	data.MaxJWTLifetimeSeconds = types.Int64Value(issuer.MaxJWTLifetimeSeconds)
	data.CreatedAt = types.StringValue(issuer.CreatedAt.Format(time.RFC3339))
	data.UpdatedAt = types.StringValue(issuer.UpdatedAt.Format(time.RFC3339))

	if issuer.CreatedByActorID != "" {
		data.CreatedByActorID = types.StringValue(issuer.CreatedByActorID)
	} else {
		data.CreatedByActorID = types.StringNull()
	}

	if issuer.UpdatedByActorID != "" {
		data.UpdatedByActorID = types.StringValue(issuer.UpdatedByActorID)
	} else {
		data.UpdatedByActorID = types.StringNull()
	}

	if issuer.ArchivedAt.IsZero() {
		data.ArchivedAt = types.StringNull()
	} else {
		data.ArchivedAt = types.StringValue(issuer.ArchivedAt.Format(time.RFC3339))
	}

	if issuer.ArchivedByActorID != "" {
		data.ArchivedByActorID = types.StringValue(issuer.ArchivedByActorID)
	} else {
		data.ArchivedByActorID = types.StringNull()
	}

	if issuer.JWKSPollingDisabledAt.IsZero() {
		data.JWKSPollingDisabledAt = types.StringNull()
	} else {
		data.JWKSPollingDisabledAt = types.StringValue(issuer.JWKSPollingDisabledAt.Format(time.RFC3339))
	}

	jwksObj, d := mapFederationIssuerDataSourceJWKS(issuer.JWKS)
	diags.Append(d...)
	data.JWKS = jwksObj

	pollStatusObj, d := mapFederationIssuerDataSourcePollStatus(issuer.PollStatus)
	diags.Append(d...)
	data.PollStatus = pollStatusObj

	return diags
}

// mapFederationIssuerDataSourceJWKS maps the jwks union onto its flattened
// Terraform object. All three JWKS shapes (discovery, explicit_url, inline)
// share one object type, so fields that don't apply to the active type stay
// null.
//
// keys is read from jwks.JSON.Keys.Raw() (the raw JSON the API returned for
// that field) rather than json.Marshal(jwks.Keys), per the jsontypes.Normalized
// convention: re-marshaling the decoded []map[string]any would risk drifting
// from what the API actually sent if the SDK ever changes how it decodes it.
func mapFederationIssuerDataSourceJWKS(jwks anthropic.BetaFederationIssuerJWKSUnion) (types.Object, diag.Diagnostics) {
	var diags diag.Diagnostics

	discoveryBase := types.StringNull()
	if jwks.DiscoveryBase != "" {
		discoveryBase = types.StringValue(jwks.DiscoveryBase)
	}

	url := types.StringNull()
	if jwks.URL != "" {
		url = types.StringValue(jwks.URL)
	}

	caCertPEM := types.StringNull()
	if jwks.CACertPEM != "" {
		caCertPEM = types.StringValue(jwks.CACertPEM)
	}

	keys := jsontypes.NewNormalizedNull()
	if raw := jwks.JSON.Keys.Raw(); raw != "" && raw != "null" {
		keys = jsontypes.NewNormalizedValue(raw)
	}

	obj, d := types.ObjectValue(federationIssuerDataSourceJWKSAttrTypes, map[string]attr.Value{
		"type":           types.StringValue(jwks.Type),
		"discovery_base": discoveryBase,
		"url":            url,
		"keys":           keys,
		"ca_cert_pem":    caCertPEM,
	})
	diags.Append(d...)
	return obj, diags
}

// mapFederationIssuerDataSourcePollStatus maps the poll_status object.
// It stays data-source-local per the spec: the resource omits poll_status
// entirely, since a resource only refreshes on plan/refresh cycles rather
// than the always-fresh read a data source performs.
func mapFederationIssuerDataSourcePollStatus(status anthropic.BetaFederationIssuerPollStatus) (types.Object, diag.Diagnostics) {
	var diags diag.Diagnostics

	lastFetchedAt := types.StringNull()
	if !status.LastFetchedAt.IsZero() {
		lastFetchedAt = types.StringValue(status.LastFetchedAt.Format(time.RFC3339))
	}

	nextPollAt := types.StringNull()
	if !status.NextPollAt.IsZero() {
		nextPollAt = types.StringValue(status.NextPollAt.Format(time.RFC3339))
	}

	obj, d := types.ObjectValue(federationIssuerDataSourcePollStatusAttrTypes, map[string]attr.Value{
		"consecutive_failures": types.Int64Value(status.ConsecutiveFailures),
		"last_fetched_at":      lastFetchedAt,
		"next_poll_at":         nextPollAt,
	})
	diags.Append(d...)
	return obj, diags
}
