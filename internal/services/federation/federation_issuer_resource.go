// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &FederationIssuerResource{}
var _ resource.ResourceWithImportState = &FederationIssuerResource{}
var _ resource.ResourceWithConfigValidators = &FederationIssuerResource{}

func NewFederationIssuerResource() resource.Resource {
	return &FederationIssuerResource{}
}

// federationIssuerNameRegexp matches the API's slug format for federation
// issuer names: lowercase letters, digits, and hyphens only.
var federationIssuerNameRegexp = regexp.MustCompile(`^[a-z0-9-]+$`)

// FederationIssuerResource defines the resource implementation.
type FederationIssuerResource struct {
	client *providerdata.OAuthClient
}

// --- Terraform data models ---

type FederationIssuerResourceModel struct {
	ID                    types.String `tfsdk:"id"`
	Name                  types.String `tfsdk:"name"`
	IssuerURL             types.String `tfsdk:"issuer_url"`
	JWKS                  types.Object `tfsdk:"jwks"`
	CheckJTI              types.Bool   `tfsdk:"check_jti"`
	MaxJWTLifetimeSeconds types.Int64  `tfsdk:"max_jwt_lifetime_seconds"`
	JWKSPollingDisabledAt types.String `tfsdk:"jwks_polling_disabled_at"`
	CreatedAt             types.String `tfsdk:"created_at"`
	UpdatedAt             types.String `tfsdk:"updated_at"`
	ArchivedAt            types.String `tfsdk:"archived_at"`
	CreatedByActorID      types.String `tfsdk:"created_by_actor_id"`
	UpdatedByActorID      types.String `tfsdk:"updated_by_actor_id"`
	ArchivedByActorID     types.String `tfsdk:"archived_by_actor_id"`
}

// federationIssuerJWKSModel holds the nested jwks block. Only the fields
// relevant to `type` are populated at any given time; the rest are null.
type federationIssuerJWKSModel struct {
	Type          types.String         `tfsdk:"type"`
	DiscoveryBase types.String         `tfsdk:"discovery_base"`
	URL           types.String         `tfsdk:"url"`
	Keys          jsontypes.Normalized `tfsdk:"keys"`
	CACertPEM     types.String         `tfsdk:"ca_cert_pem"`
}

// --- Attribute type maps for nested objects ---

var federationIssuerJWKSAttrTypes = map[string]attr.Type{
	"type":           types.StringType,
	"discovery_base": types.StringType,
	"url":            types.StringType,
	"keys":           jsontypes.NormalizedType{},
	"ca_cert_pem":    types.StringType,
}

// federationIssuerJWKSDiscoveryDefault is the server-side default jwks shape
// ({"type": "discovery"}) applied when the attribute is entirely omitted from
// configuration.
var federationIssuerJWKSDiscoveryDefault = types.ObjectValueMust(federationIssuerJWKSAttrTypes, map[string]attr.Value{
	"type":           types.StringValue("discovery"),
	"discovery_base": types.StringNull(),
	"url":            types.StringNull(),
	"keys":           jsontypes.NewNormalizedNull(),
	"ca_cert_pem":    types.StringNull(),
})

// --- Schema ---

func (r *FederationIssuerResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_federation_issuer"
}

func (r *FederationIssuerResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Registers an OIDC identity provider that Anthropic trusts for Workload Identity Federation (beta) in your organization. " +
			"Requires an org:admin OAuth bearer token (`auth_token` / `ANTHROPIC_AUTH_TOKEN`); Admin API keys are not accepted.\n\n" +
			"> **No hard delete**: destroying this resource always archives the issuer (`POST .../archive`). Archiving is rejected with a 400 " +
			"while a live federation rule still references the issuer; archive or recreate those rules first.",
		Attributes: map[string]schema.Attribute{
			// --- Identity ---
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Unique federation issuer identifier (e.g. `fdis_...`).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},

			// --- Required (updatable) ---
			"name": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Admin-chosen slug identifier. Lowercase letters, digits, and hyphens only. " +
					"Unique within the organization; a duplicate name returns a 409 from the API.",
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 255),
					stringvalidator.RegexMatches(federationIssuerNameRegexp, "must contain only lowercase letters, digits, and hyphens"),
				},
			},
			"issuer_url": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "The `iss` claim value to match against incoming JWTs. When `jwks.type` is `discovery` and no `discovery_base` is set, " +
					"this URL must be publicly reachable over HTTPS so Anthropic can fetch the OIDC discovery document; for `explicit_url` and `inline` " +
					"modes it is only string-compared to the JWT's `iss` claim and may be an internal URL. Not validated client-side beyond being non-empty.",
			},

			// --- jwks (discriminated union) ---
			"jwks": schema.SingleNestedAttribute{
				Optional: true,
				Computed: true,
				Default:  objectdefault.StaticValue(federationIssuerJWKSDiscoveryDefault),
				MarkdownDescription: "How the issuer's signing keys are obtained. Defaults to `{type = \"discovery\"}`. One of three shapes selected by `type`: " +
					"`discovery` (resolve keys through OIDC discovery), `explicit_url` (fetch keys from a fixed JWKS URL), or `inline` (a static key set).",
				Attributes: map[string]schema.Attribute{
					"type": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "One of `discovery`, `explicit_url`, or `inline`.",
						Validators:          []validator.String{stringvalidator.OneOf("discovery", "explicit_url", "inline")},
					},
					"discovery_base": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "Set when the discovery URL differs from `issuer_url`. Only applicable when `type` is `discovery`.",
					},
					"url": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "JWKS endpoint. Required when `type` is `explicit_url`; must be absent otherwise.",
					},
					"keys": schema.StringAttribute{
						Optional:            true,
						CustomType:          jsontypes.NormalizedType{},
						MarkdownDescription: "JSON array of inline JWK objects. Required when `type` is `inline`; must be absent otherwise.",
					},
					"ca_cert_pem": schema.StringAttribute{
						Optional: true,
						MarkdownDescription: "Optional custom CA (PEM) for TLS verification of the JWKS fetch. Only applicable when `type` is " +
							"`discovery` or `explicit_url`; must be absent for `inline` (no network fetch occurs).",
					},
				},
			},

			// --- Optional+Computed (server defaults) ---
			"check_jti": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(true),
				MarkdownDescription: "Whether the jwt-bearer exchange enforces JTI single-use (replay protection) for tokens from this issuer. " +
					"Applies only to assertions carrying a `jti` claim. Default: `true`.",
			},
			"max_jwt_lifetime_seconds": schema.Int64Attribute{
				Optional: true,
				Computed: true,
				Default:  int64default.StaticInt64(3600),
				MarkdownDescription: "Maximum allowed `iat`→`exp` spread for assertions from this issuer, in seconds (up to 49h). " +
					"Assertions must carry both `iat` and `exp`; a missing `iat` is rejected. Default: `3600` (1h).",
				Validators: []validator.Int64{int64validator.Between(1, 176400)},
			},

			// --- Computed ---
			"jwks_polling_disabled_at": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "Set when Anthropic's background JWKS poller has paused polling for this issuer after repeated fetch failures. " +
					"Null while polling is active. Re-enabling requires the update-only `jwks_polling_disabled: false` toggle on the API, which this " +
					"resource does not expose (see the provider documentation); use the API directly if this needs unpausing.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Creation timestamp (RFC 3339).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Last update timestamp (RFC 3339).",
			},
			"archived_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Archive timestamp (RFC 3339). Null if the issuer has not been archived.",
			},
			"created_by_actor_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Tagged ID (`user_...`/`svac_...`) of the actor that created this issuer.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"updated_by_actor_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Tagged ID (`user_...`/`svac_...`) of the actor that last updated this issuer.",
			},
			"archived_by_actor_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Tagged ID (`user_...`/`svac_...`) of the actor that archived this issuer. Null if not archived.",
			},
		},
	}
}

// --- ConfigValidators ---

func (r *FederationIssuerResource) ConfigValidators(_ context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		&federationIssuerConfigValidator{},
	}
}

// federationIssuerConfigValidator validates the jwks cross-attribute constraints based on jwks.type.
type federationIssuerConfigValidator struct{}

func (v *federationIssuerConfigValidator) Description(_ context.Context) string {
	return "Validates that the correct jwks attributes are set for the given jwks.type."
}

func (v *federationIssuerConfigValidator) MarkdownDescription(_ context.Context) string {
	return v.Description(context.Background())
}

func (v *federationIssuerConfigValidator) ValidateResource(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var data FederationIssuerResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// isSet reports whether an optional attribute is definitively provided in
	// config. An Unknown value (e.g. an unresolved var/output ref) is treated
	// as neither set nor missing, so it never trips a required- or
	// conflicting-attribute check.
	isSet := func(v attr.Value) bool { return !v.IsNull() && !v.IsUnknown() }
	// isMissing reports whether a required attribute is definitively absent.
	// Unknown is not missing — it may resolve to a value at apply time.
	isMissing := func(v attr.Value) bool { return v.IsNull() && !v.IsUnknown() }

	// jwks entirely omitted or unresolved: nothing to validate yet (the schema
	// default supplies {type = "discovery"} once the plan is computed).
	if !isSet(data.JWKS) {
		return
	}

	var jwks federationIssuerJWKSModel
	resp.Diagnostics.Append(data.JWKS.As(ctx, &jwks, basetypes.ObjectAsOptions{})...)
	if resp.Diagnostics.HasError() {
		return
	}

	// jwks.type is Required within the nested object; the framework's own
	// schema validation already errors if it is missing from a set jwks block.
	if !isSet(jwks.Type) {
		return
	}
	jwksType := jwks.Type.ValueString()

	// Per jwks.type: the attributes that are required, plus the additional
	// ones that are merely allowed. Any jwks attribute in neither set is
	// forbidden for that type. Driving the checks from this table keeps the
	// three cases in lockstep instead of hand-written conditionals.
	type jwksFieldRules struct {
		required []string
		optional []string
	}
	rules := map[string]jwksFieldRules{
		"discovery":    {optional: []string{"discovery_base", "ca_cert_pem"}},
		"explicit_url": {required: []string{"url"}, optional: []string{"ca_cert_pem"}},
		"inline":       {required: []string{"keys"}},
	}
	// Stable order so emitted diagnostics are deterministic.
	fieldOrder := []string{"discovery_base", "url", "keys", "ca_cert_pem"}
	fieldValues := map[string]attr.Value{
		"discovery_base": jwks.DiscoveryBase,
		"url":            jwks.URL,
		"keys":           jwks.Keys,
		"ca_cert_pem":    jwks.CACertPEM,
	}

	rule, ok := rules[jwksType]
	if !ok {
		return // unknown type already rejected by the OneOf validator on jwks.type
	}
	required := make(map[string]bool, len(rule.required))
	allowed := make(map[string]bool, len(rule.required)+len(rule.optional))
	for _, n := range rule.required {
		required[n] = true
		allowed[n] = true
	}
	for _, n := range rule.optional {
		allowed[n] = true
	}

	for _, name := range fieldOrder {
		switch {
		case required[name]:
			if isMissing(fieldValues[name]) {
				resp.Diagnostics.AddAttributeError(
					path.Root("jwks").AtName(name),
					"Missing required attribute",
					fmt.Sprintf("%q is required when jwks.type is %q.", name, jwksType),
				)
			}
		case !allowed[name]:
			if isSet(fieldValues[name]) {
				resp.Diagnostics.AddAttributeError(
					path.Root("jwks").AtName(name),
					"Conflicting attribute",
					fmt.Sprintf("%q must not be set when jwks.type is %q.", name, jwksType),
				)
			}
		}
	}
}

// --- Configure ---

func (r *FederationIssuerResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	pd, ok := req.ProviderData.(*providerdata.ProviderData)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *providerdata.ProviderData, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}

	if !providerrors.RequireOAuthResourceClient(pd.OAuthClient, &resp.Diagnostics) {
		return
	}

	r.client = pd.OAuthClient
}

// --- Create ---

func (r *FederationIssuerResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data FederationIssuerResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	discovery, explicitURL, inline, diags := buildJWKSParams(ctx, data.JWKS)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	params := anthropic.BetaOrganizationFederationIssuerNewParams{
		Name:      data.Name.ValueString(),
		IssuerURL: data.IssuerURL.ValueString(),
		JWKS: anthropic.BetaOrganizationFederationIssuerNewParamsJWKSUnion{
			OfDiscovery:   discovery,
			OfExplicitURL: explicitURL,
			OfInline:      inline,
		},
	}

	if !data.CheckJTI.IsNull() && !data.CheckJTI.IsUnknown() {
		params.CheckJTI = param.NewOpt(data.CheckJTI.ValueBool())
	}
	if !data.MaxJWTLifetimeSeconds.IsNull() && !data.MaxJWTLifetimeSeconds.IsUnknown() {
		params.MaxJWTLifetimeSeconds = param.NewOpt(data.MaxJWTLifetimeSeconds.ValueInt64())
	}

	issuer, err := r.client.Beta.Organization.Federation.Issuers.New(ctx, params)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create federation issuer: %s", err))
		return
	}

	resp.Diagnostics.Append(mapFederationIssuerToState(ctx, issuer, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Read ---

func (r *FederationIssuerResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data FederationIssuerResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	issuer, err := r.client.Beta.Organization.Federation.Issuers.Get(ctx, data.ID.ValueString(), anthropic.BetaOrganizationFederationIssuerGetParams{})
	if err != nil {
		// The issuer was deleted out-of-band: drop it from state so the next
		// plan recreates it instead of erroring forever.
		var apierr *anthropic.Error
		if errors.As(err, &apierr) && apierr.StatusCode == 404 {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read federation issuer: %s", err))
		return
	}

	resp.Diagnostics.Append(mapFederationIssuerToState(ctx, issuer, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Update ---

func (r *FederationIssuerResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan FederationIssuerResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state FederationIssuerResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	params := anthropic.BetaOrganizationFederationIssuerUpdateParams{}

	if !plan.Name.Equal(state.Name) {
		params.Name = param.NewOpt(plan.Name.ValueString())
	}
	if !plan.IssuerURL.Equal(state.IssuerURL) {
		params.IssuerURL = param.NewOpt(plan.IssuerURL.ValueString())
	}
	if !plan.CheckJTI.Equal(state.CheckJTI) {
		params.CheckJTI = param.NewOpt(plan.CheckJTI.ValueBool())
	}
	if !plan.MaxJWTLifetimeSeconds.Equal(state.MaxJWTLifetimeSeconds) {
		params.MaxJWTLifetimeSeconds = param.NewOpt(plan.MaxJWTLifetimeSeconds.ValueInt64())
	}
	// Setting jwks replaces the full JWKS shape at once, so it is only sent
	// when it actually changed.
	if !plan.JWKS.Equal(state.JWKS) {
		discovery, explicitURL, inline, diags := buildJWKSParams(ctx, plan.JWKS)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		params.JWKS = anthropic.BetaOrganizationFederationIssuerUpdateParamsJWKSUnion{
			OfDiscovery:   discovery,
			OfExplicitURL: explicitURL,
			OfInline:      inline,
		}
	}

	issuer, err := r.client.Beta.Organization.Federation.Issuers.Update(ctx, state.ID.ValueString(), params)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update federation issuer: %s", err))
		return
	}

	resp.Diagnostics.Append(mapFederationIssuerToState(ctx, issuer, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// --- Delete ---

// Delete always archives: the API has no hard-delete endpoint for federation
// issuers. Archive is idempotent and returns a 400 while a live rule still
// references the issuer.
func (r *FederationIssuerResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data FederationIssuerResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.Beta.Organization.Federation.Issuers.Archive(ctx, data.ID.ValueString(), anthropic.BetaOrganizationFederationIssuerArchiveParams{})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to archive federation issuer: %s", err))
	}
}

// --- ImportState ---

func (r *FederationIssuerResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// ============================================================================
// Helper functions
// ============================================================================

// buildJWKSParams builds the three possible jwks param variants from a
// Terraform jwks object. Exactly one of the returned pointers is non-nil,
// matching whichever variant the union is built with in the caller (the New
// and Update param unions embed the same three variant struct types).
func buildJWKSParams(ctx context.Context, jwksObj types.Object) (
	discovery *anthropic.BetaJWKSDiscoveryParam,
	explicitURL *anthropic.BetaJWKSExplicitURLParam,
	inline *anthropic.BetaJWKSInlineParam,
	diags diag.Diagnostics,
) {
	var jwks federationIssuerJWKSModel
	diags.Append(jwksObj.As(ctx, &jwks, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return nil, nil, nil, diags
	}

	switch jwks.Type.ValueString() {
	case "explicit_url":
		p := &anthropic.BetaJWKSExplicitURLParam{
			URL: jwks.URL.ValueString(),
		}
		if !jwks.CACertPEM.IsNull() && !jwks.CACertPEM.IsUnknown() {
			p.CACertPEM = param.NewOpt(jwks.CACertPEM.ValueString())
		}
		return nil, p, nil, diags

	case "inline":
		var keys []map[string]any
		if !jwks.Keys.IsNull() && !jwks.Keys.IsUnknown() {
			if err := json.Unmarshal([]byte(jwks.Keys.ValueString()), &keys); err != nil {
				diags.AddAttributeError(path.Root("jwks").AtName("keys"), "Invalid keys", fmt.Sprintf("Failed to parse keys as a JSON array: %s", err))
				return nil, nil, nil, diags
			}
		}
		p := &anthropic.BetaJWKSInlineParam{Keys: keys}
		return nil, nil, p, diags

	default: // "discovery" (also the fallback: the OneOf validator rejects anything else at plan time)
		p := &anthropic.BetaJWKSDiscoveryParam{}
		if !jwks.DiscoveryBase.IsNull() && !jwks.DiscoveryBase.IsUnknown() {
			p.DiscoveryBase = param.NewOpt(jwks.DiscoveryBase.ValueString())
		}
		if !jwks.CACertPEM.IsNull() && !jwks.CACertPEM.IsUnknown() {
			p.CACertPEM = param.NewOpt(jwks.CACertPEM.ValueString())
		}
		return p, nil, nil, diags
	}
}

// mapJWKSResponseToObject maps the flat BetaFederationIssuerJWKSUnion response
// back to a Terraform jwks object, keeping only the fields relevant to the
// returned type.
func mapJWKSResponseToObject(jwks anthropic.BetaFederationIssuerJWKSUnion) (types.Object, diag.Diagnostics) {
	var diags diag.Diagnostics

	discoveryBase := types.StringNull()
	url := types.StringNull()
	caCertPEM := types.StringNull()
	keys := jsontypes.NewNormalizedNull()

	switch jwks.Type {
	case "discovery":
		if jwks.DiscoveryBase != "" {
			discoveryBase = types.StringValue(jwks.DiscoveryBase)
		}
		if jwks.CACertPEM != "" {
			caCertPEM = types.StringValue(jwks.CACertPEM)
		}
	case "explicit_url":
		url = types.StringValue(jwks.URL)
		if jwks.CACertPEM != "" {
			caCertPEM = types.StringValue(jwks.CACertPEM)
		}
	case "inline":
		// Read the exact wire bytes for the "keys" field rather than
		// re-marshaling jwks.Keys, per the jsontypes.Normalized convention:
		// re-marshaling an SDK struct can silently drift from the API's JSON
		// on a field-order/whitespace change.
		if jwks.JSON.Keys.Valid() {
			if raw := jwks.JSON.Keys.Raw(); raw != "" && raw != "null" {
				keys = jsontypes.NewNormalizedValue(raw)
			}
		}
	}

	obj, d := types.ObjectValue(federationIssuerJWKSAttrTypes, map[string]attr.Value{
		"type":           types.StringValue(jwks.Type),
		"discovery_base": discoveryBase,
		"url":            url,
		"keys":           keys,
		"ca_cert_pem":    caCertPEM,
	})
	diags.Append(d...)
	return obj, diags
}

// mapFederationIssuerToState maps the API response to the Terraform state model.
func mapFederationIssuerToState(_ context.Context, issuer *anthropic.BetaFederationIssuer, data *FederationIssuerResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics

	data.ID = types.StringValue(issuer.ID)
	data.Name = types.StringValue(issuer.Name)
	data.IssuerURL = types.StringValue(issuer.IssuerURL)
	data.CheckJTI = types.BoolValue(issuer.CheckJTI)
	data.MaxJWTLifetimeSeconds = types.Int64Value(issuer.MaxJWTLifetimeSeconds)

	data.CreatedAt = formatTimeOrNull(issuer.CreatedAt)
	data.UpdatedAt = formatTimeOrNull(issuer.UpdatedAt)
	data.ArchivedAt = formatTimeOrNull(issuer.ArchivedAt)
	data.JWKSPollingDisabledAt = formatTimeOrNull(issuer.JWKSPollingDisabledAt)

	data.CreatedByActorID = stringOrNull(issuer.CreatedByActorID)
	data.UpdatedByActorID = stringOrNull(issuer.UpdatedByActorID)
	data.ArchivedByActorID = stringOrNull(issuer.ArchivedByActorID)

	jwksObj, d := mapJWKSResponseToObject(issuer.JWKS)
	diags.Append(d...)
	data.JWKS = jwksObj

	return diags
}

// formatTimeOrNull formats a zero-valuable API timestamp as null, matching
// the "" / zero-time -> null convention used for every optional timestamp in
// this resource.
func formatTimeOrNull(t time.Time) types.String {
	if t.IsZero() {
		return types.StringNull()
	}
	return types.StringValue(t.Format(time.RFC3339))
}

// stringOrNull maps an empty API string (e.g. an actor ID before an action
// has happened, such as archived_by_actor_id on a live issuer) to null.
func stringOrNull(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}
