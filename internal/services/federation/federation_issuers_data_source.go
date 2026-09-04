// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation

import (
	"context"
	"fmt"
	"time"

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

// federationIssuersJWKSAttrTypes describes the attribute types of the "jwks"
// nested object within each issuer entry.
//
// Named with the "federationIssuers" prefix (rather than a generic
// "federationJWKSAttrTypes") because sibling branches for the singular
// anthropic_federation_issuer data source/resource define their own
// equivalents in the same "federation" package; the prefix keeps every
// unexported symbol collision-free until those branches merge and a dedupe
// refactor lands.
var federationIssuersJWKSAttrTypes = map[string]attr.Type{
	"type":           types.StringType,
	"ca_cert_pem":    types.StringType,
	"discovery_base": types.StringType,
	"url":            types.StringType,
	"keys":           jsontypes.NormalizedType{},
}

// federationIssuersPollStatusAttrTypes describes the attribute types of the
// "poll_status" nested object within each issuer entry.
var federationIssuersPollStatusAttrTypes = map[string]attr.Type{
	"consecutive_failures": types.Int64Type,
	"last_fetched_at":      types.StringType,
	"next_poll_at":         types.StringType,
}

// federationIssuersListItemAttrTypes describes the attribute types of each
// element in the "issuers" list.
var federationIssuersListItemAttrTypes = map[string]attr.Type{
	"id":                       types.StringType,
	"issuer_url":               types.StringType,
	"name":                     types.StringType,
	"check_jti":                types.BoolType,
	"max_jwt_lifetime_seconds": types.Int64Type,
	"jwks":                     types.ObjectType{AttrTypes: federationIssuersJWKSAttrTypes},
	"jwks_polling_disabled_at": types.StringType,
	"poll_status":              types.ObjectType{AttrTypes: federationIssuersPollStatusAttrTypes},
	"created_at":               types.StringType,
	"created_by_actor_id":      types.StringType,
	"updated_at":               types.StringType,
	"updated_by_actor_id":      types.StringType,
	"archived_at":              types.StringType,
	"archived_by_actor_id":     types.StringType,
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
		obj, diags := mapFederationIssuersListEntry(&issuer)
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

	issuersList, diags := types.ListValue(types.ObjectType{AttrTypes: federationIssuersListItemAttrTypes}, issuerObjs)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.Issuers = issuersList

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// mapFederationIssuersListEntry converts an API federation issuer response to
// a Terraform object value for inclusion in the "issuers" list.
func mapFederationIssuersListEntry(issuer *anthropic.BetaFederationIssuer) (attr.Value, diag.Diagnostics) {
	var diags diag.Diagnostics

	jwksObj, d := mapFederationIssuersJWKS(issuer.JWKS)
	diags.Append(d...)

	pollStatusObj, d := mapFederationIssuersPollStatus(issuer.PollStatus)
	diags.Append(d...)

	jwksPollingDisabledAt := types.StringNull()
	if !issuer.JWKSPollingDisabledAt.IsZero() {
		jwksPollingDisabledAt = types.StringValue(issuer.JWKSPollingDisabledAt.Format(time.RFC3339))
	}

	archivedAt := types.StringNull()
	if !issuer.ArchivedAt.IsZero() {
		archivedAt = types.StringValue(issuer.ArchivedAt.Format(time.RFC3339))
	}

	archivedByActorID := types.StringNull()
	if issuer.ArchivedByActorID != "" {
		archivedByActorID = types.StringValue(issuer.ArchivedByActorID)
	}

	obj, d := types.ObjectValue(federationIssuersListItemAttrTypes, map[string]attr.Value{
		"id":                       types.StringValue(issuer.ID),
		"issuer_url":               types.StringValue(issuer.IssuerURL),
		"name":                     types.StringValue(issuer.Name),
		"check_jti":                types.BoolValue(issuer.CheckJTI),
		"max_jwt_lifetime_seconds": types.Int64Value(issuer.MaxJWTLifetimeSeconds),
		"jwks":                     jwksObj,
		"jwks_polling_disabled_at": jwksPollingDisabledAt,
		"poll_status":              pollStatusObj,
		"created_at":               types.StringValue(issuer.CreatedAt.Format(time.RFC3339)),
		"created_by_actor_id":      types.StringValue(issuer.CreatedByActorID),
		"updated_at":               types.StringValue(issuer.UpdatedAt.Format(time.RFC3339)),
		"updated_by_actor_id":      types.StringValue(issuer.UpdatedByActorID),
		"archived_at":              archivedAt,
		"archived_by_actor_id":     archivedByActorID,
	})
	diags.Append(d...)
	return obj, diags
}

// mapFederationIssuersJWKS converts the API's flat JWKS union to a Terraform
// object. The union already carries every variant's fields flattened onto one
// struct (response-side union types are not per-variant), so no type switch is
// needed here — only the fields relevant to the issuer's actual jwks.type will
// be non-empty.
func mapFederationIssuersJWKS(jwks anthropic.BetaFederationIssuerJWKSUnion) (types.Object, diag.Diagnostics) {
	var diags diag.Diagnostics

	caCertPEM := types.StringNull()
	if jwks.CACertPEM != "" {
		caCertPEM = types.StringValue(jwks.CACertPEM)
	}

	discoveryBase := types.StringNull()
	if jwks.DiscoveryBase != "" {
		discoveryBase = types.StringValue(jwks.DiscoveryBase)
	}

	url := types.StringNull()
	if jwks.URL != "" {
		url = types.StringValue(jwks.URL)
	}

	// Use the per-field raw JSON captured at unmarshal time rather than
	// re-marshalling jwks.Keys: the field is only actually present (as opposed
	// to omitted) for the "inline" variant, and Field.Raw() preserves that
	// distinction without risking key-order/whitespace drift from a fresh
	// json.Marshal.
	keys := jsontypes.NewNormalizedNull()
	if raw := jwks.JSON.Keys.Raw(); raw != "" && raw != "null" {
		keys = jsontypes.NewNormalizedValue(raw)
	}

	obj, d := types.ObjectValue(federationIssuersJWKSAttrTypes, map[string]attr.Value{
		"type":           types.StringValue(jwks.Type),
		"ca_cert_pem":    caCertPEM,
		"discovery_base": discoveryBase,
		"url":            url,
		"keys":           keys,
	})
	diags.Append(d...)
	return obj, diags
}

// mapFederationIssuersPollStatus converts the API's poll status to a Terraform object.
func mapFederationIssuersPollStatus(status anthropic.BetaFederationIssuerPollStatus) (types.Object, diag.Diagnostics) {
	var diags diag.Diagnostics

	lastFetchedAt := types.StringNull()
	if !status.LastFetchedAt.IsZero() {
		lastFetchedAt = types.StringValue(status.LastFetchedAt.Format(time.RFC3339))
	}

	nextPollAt := types.StringNull()
	if !status.NextPollAt.IsZero() {
		nextPollAt = types.StringValue(status.NextPollAt.Format(time.RFC3339))
	}

	obj, d := types.ObjectValue(federationIssuersPollStatusAttrTypes, map[string]attr.Value{
		"consecutive_failures": types.Int64Value(status.ConsecutiveFailures),
		"last_fetched_at":      lastFetchedAt,
		"next_poll_at":         nextPollAt,
	})
	diags.Append(d...)
	return obj, diags
}
