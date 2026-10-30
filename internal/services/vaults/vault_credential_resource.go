// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package vaults

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &VaultCredentialResource{}
var _ resource.ResourceWithImportState = &VaultCredentialResource{}
var _ resource.ResourceWithConfigValidators = &VaultCredentialResource{}
var _ resource.ResourceWithModifyPlan = &VaultCredentialResource{}

func NewVaultCredentialResource() resource.Resource {
	return &VaultCredentialResource{}
}

// VaultCredentialResource defines the resource implementation.
type VaultCredentialResource struct {
	client *anthropic.Client
}

// --- Terraform data models ---

// VaultCredentialResourceModel holds the Terraform state for a vault credential.
// Write-only fields (token, access_token, refresh_token, client_secret, secret_value)
// are NEVER written to state — they are read from req.Config in Create/Update only.
type VaultCredentialResourceModel struct {
	// Identity
	ID      types.String `tfsdk:"id"`
	VaultID types.String `tfsdk:"vault_id"`

	// Auth type selector (RequiresReplace)
	Type types.String `tfsdk:"type"`

	// Common optional
	DisplayName types.String `tfsdk:"display_name"`
	Metadata    types.Map    `tfsdk:"metadata"`

	// static_bearer / mcp_oauth (RequiresReplace for bearer/oauth; absent for env-var)
	MCPServerURL types.String `tfsdk:"mcp_server_url"`

	// static_bearer write-only secret
	Token types.String `tfsdk:"token"`

	// mcp_oauth fields
	AccessToken types.String `tfsdk:"access_token"`
	ExpiresAt   types.String `tfsdk:"expires_at"`
	Refresh     types.Object `tfsdk:"refresh"`

	// environment_variable fields
	SecretName        types.String `tfsdk:"secret_name"`
	SecretValue       types.String `tfsdk:"secret_value"`
	Networking        types.Object `tfsdk:"networking"`
	InjectionLocation types.Object `tfsdk:"injection_location"`

	// Rotation trigger (not a secret; stored in state)
	TokenWoVersion types.Int64 `tfsdk:"token_wo_version"`

	// Computed timestamps
	CreatedAt  types.String `tfsdk:"created_at"`
	UpdatedAt  types.String `tfsdk:"updated_at"`
	ArchivedAt types.String `tfsdk:"archived_at"`

	// Computed object type from API response (named distinctly to avoid clash with auth `type`)
	CredentialType types.String `tfsdk:"credential_type"`

	// Archive behaviour
	ArchiveOnDestroy types.Bool `tfsdk:"archive_on_destroy"`
}

// credentialRefreshModel holds the nested refresh block for mcp_oauth.
type credentialRefreshModel struct {
	ClientID          types.String `tfsdk:"client_id"`
	RefreshToken      types.String `tfsdk:"refresh_token"`
	TokenEndpoint     types.String `tfsdk:"token_endpoint"`
	Resource          types.String `tfsdk:"resource"`
	Scope             types.String `tfsdk:"scope"`
	TokenEndpointAuth types.Object `tfsdk:"token_endpoint_auth"`
}

// credentialTokenEndpointAuthModel holds the nested token_endpoint_auth block.
type credentialTokenEndpointAuthModel struct {
	Type         types.String `tfsdk:"type"`
	ClientSecret types.String `tfsdk:"client_secret"`
}

// credentialNetworkingModel holds the nested networking block for environment_variable.
type credentialNetworkingModel struct {
	Mode         types.String `tfsdk:"mode"`
	AllowedHosts types.List   `tfsdk:"allowed_hosts"`
}

// credentialInjectionLocationModel holds the nested injection_location block for
// environment_variable.
type credentialInjectionLocationModel struct {
	Header types.Bool `tfsdk:"header"`
	Body   types.Bool `tfsdk:"body"`
}

// --- Attribute type maps for nested objects ---

var credentialInjectionLocationAttrTypes = map[string]attr.Type{
	"header": types.BoolType,
	"body":   types.BoolType,
}

var credentialTokenEndpointAuthAttrTypes = map[string]attr.Type{
	"type":          types.StringType,
	"client_secret": types.StringType,
}

var credentialRefreshAttrTypes = map[string]attr.Type{
	"client_id":           types.StringType,
	"refresh_token":       types.StringType,
	"token_endpoint":      types.StringType,
	"resource":            types.StringType,
	"scope":               types.StringType,
	"token_endpoint_auth": types.ObjectType{AttrTypes: credentialTokenEndpointAuthAttrTypes},
}

var credentialNetworkingAttrTypes = map[string]attr.Type{
	"mode":          types.StringType,
	"allowed_hosts": types.ListType{ElemType: types.StringType},
}

// --- Schema ---

func (r *VaultCredentialResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_vault_credential"
}

func (r *VaultCredentialResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Creates and manages a vault credential for Anthropic Managed Agents (beta). " +
			"A credential stores authentication material (bearer tokens, OAuth tokens, or environment variable secrets) " +
			"that agents can use when calling MCP servers.\n\n" +
			"> **SECURITY NOTE**: Credential material (`token` / `access_token` / `refresh_token` / `client_secret` / `secret_value`) " +
			"is a write-only argument, never persisted to Terraform state. Use `token_wo_version` to rotate secrets. " +
			"Anthropic stores and refreshes the credential server-side; the secret is never logged.\n\n" +
			"> **Limits**: Max 20 credentials per vault. `allowed_hosts` accepts bare hostnames, IPv4 addresses, " +
			"or `*.`-wildcards (max 16 entries; no URLs, ports, paths, or IPv6 addresses).",
		Attributes: map[string]schema.Attribute{
			// --- Identity ---
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Unique credential identifier (e.g. `vcrd_...`).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"vault_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Identifier of the vault this credential belongs to.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},

			// --- Auth type selector ---
			"type": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Auth type. One of `static_bearer`, `mcp_oauth`, or `environment_variable`.",
				Validators:          []validator.String{stringvalidator.OneOf("static_bearer", "mcp_oauth", "environment_variable")},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},

			// --- Common optional ---
			"display_name": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Human-readable name for the credential (up to 255 characters).",
			},
			"metadata": schema.MapAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Arbitrary key-value metadata. Maximum 16 pairs; keys up to 64 chars; values up to 512 chars.",
			},

			// --- static_bearer / mcp_oauth (RequiresReplace; absent for environment_variable) ---
			"mcp_server_url": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "URL of the MCP server this credential authenticates against. Required for `static_bearer` and `mcp_oauth`; must be absent for `environment_variable`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},

			// --- static_bearer write-only secret ---
			"token": schema.StringAttribute{
				Optional:            true,
				WriteOnly:           true,
				Sensitive:           true,
				MarkdownDescription: "Static bearer token value. Write-only; never stored in state. Required for `static_bearer`. Use `token_wo_version` to trigger rotation.",
			},

			// --- mcp_oauth fields ---
			"access_token": schema.StringAttribute{
				Optional:            true,
				WriteOnly:           true,
				Sensitive:           true,
				MarkdownDescription: "OAuth access token. Write-only; never stored in state. Required for `mcp_oauth`. Use `token_wo_version` to trigger rotation.",
			},
			"expires_at": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Optional expiry time of the access token (RFC 3339). Applicable for `mcp_oauth`.",
			},
			"refresh": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "OAuth refresh token configuration. Applicable for `mcp_oauth`.",
				Attributes: map[string]schema.Attribute{
					"client_id": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "OAuth client ID. Immutable: changing it forces replacement of the credential (the update API cannot modify it).",
						PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
					},
					"refresh_token": schema.StringAttribute{
						Required:            true,
						WriteOnly:           true,
						Sensitive:           true,
						MarkdownDescription: "OAuth refresh token. Write-only; never stored in state.",
					},
					"token_endpoint": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "Token endpoint URL used to refresh the access token. Immutable: changing it forces replacement of the credential (the update API cannot modify it).",
						PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
					},
					"resource": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "OAuth resource indicator. Immutable: changing it forces replacement of the credential (the update API cannot modify it).",
						PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
					},
					"scope": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "OAuth scope for the refresh request.",
					},
					"token_endpoint_auth": schema.SingleNestedAttribute{
						Required:            true,
						MarkdownDescription: "Token endpoint authentication method.",
						Attributes: map[string]schema.Attribute{
							"type": schema.StringAttribute{
								Required:            true,
								MarkdownDescription: "Authentication method type. One of `none`, `client_secret_basic`, `client_secret_post`. Immutable: changing it forces replacement of the credential (the update API has no `none` variant and keeps the stored type when sent the other `client_secret_*` one).",
								Validators:          []validator.String{stringvalidator.OneOf("none", "client_secret_basic", "client_secret_post")},
								PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
							},
							"client_secret": schema.StringAttribute{
								Optional:            true,
								WriteOnly:           true,
								Sensitive:           true,
								MarkdownDescription: "OAuth client secret. Write-only; required for `client_secret_basic` and `client_secret_post`.",
							},
						},
					},
				},
			},

			// --- environment_variable fields ---
			"secret_name": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Name of the environment variable. Immutable after creation. Required for `environment_variable`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"secret_value": schema.StringAttribute{
				Optional:            true,
				WriteOnly:           true,
				Sensitive:           true,
				MarkdownDescription: "Secret value for the environment variable. Write-only; never stored in state. Required for `environment_variable`. Use `token_wo_version` to trigger rotation.",
			},
			"networking": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Networking scope for the environment variable credential. Required for `environment_variable`.",
				Attributes: map[string]schema.Attribute{
					"mode": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "Networking mode. One of `unrestricted` or `limited`.",
						Validators:          []validator.String{stringvalidator.OneOf("unrestricted", "limited")},
					},
					"allowed_hosts": schema.ListAttribute{
						Optional:            true,
						ElementType:         types.StringType,
						MarkdownDescription: "Hosts on which the secret is substituted. Required when `mode` is `limited`. Each entry is a bare hostname (`api.example.com`), an IPv4 address, or a `*.`-prefixed wildcard. Maximum 16 entries.",
						Validators:          []validator.List{listvalidator.SizeAtMost(16)},
					},
				},
			},
			"injection_location": schema.SingleNestedAttribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "Where in an outbound request the secret is substituted. Applicable for `environment_variable`. " +
					"Without the block, the API enables both locations on create; in the block, a field left out defaults to `false` " +
					"and at least one of `header` and `body` must be `true`. Removing the block keeps the current value, " +
					"until the credential is replaced or recreated: the new credential gets both locations again, so keep the block " +
					"to hold a narrower value. " +
					"Enabling only `header` is the narrower choice: request bodies are often built from content the agent works with.",
				// UseNonNullStateForUnknown, not UseStateForUnknown: state written
				// before this attribute existed holds null, and planning that null
				// for an environment_variable credential would fail the apply once
				// Update stores the location the API returns.
				PlanModifiers: []planmodifier.Object{
					objectplanmodifier.UseNonNullStateForUnknown(),
					injectionLocationNullUnlessEnvVar(),
				},
				Attributes: map[string]schema.Attribute{
					"header": schema.BoolAttribute{
						Optional:            true,
						Computed:            true,
						Default:             booldefault.StaticBool(false),
						MarkdownDescription: "Substitute the secret when its placeholder appears in a request header value. Defaults to `false` when `injection_location` is set.",
					},
					"body": schema.BoolAttribute{
						Optional:            true,
						Computed:            true,
						Default:             booldefault.StaticBool(false),
						MarkdownDescription: "Substitute the secret when its placeholder appears in the request body. Defaults to `false` when `injection_location` is set.",
					},
				},
			},

			// --- Rotation trigger ---
			"token_wo_version": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "Increment this value to trigger re-upload of the write-only secret(s) on the next `terraform apply`. Terraform cannot diff write-only values, so this field is the rotation mechanism.",
			},

			// --- Computed timestamps ---
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
				MarkdownDescription: "Archive timestamp (RFC 3339). Null if not archived.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},

			// --- Computed object type ---
			"credential_type": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Object type returned by the API. Always `vault_credential`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},

			// --- Archive behaviour ---
			"archive_on_destroy": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "If `true`, destroying this resource archives the credential instead of permanently deleting it. Default: `false`.",
				PlanModifiers:       []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

// --- ConfigValidators ---

func (r *VaultCredentialResource) ConfigValidators(_ context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		&vaultCredentialConfigValidator{},
	}
}

// vaultCredentialConfigValidator validates cross-attribute constraints based on `type`.
type vaultCredentialConfigValidator struct{}

func (v *vaultCredentialConfigValidator) Description(_ context.Context) string {
	return "Validates that the correct credential attributes are set for the given auth type."
}

func (v *vaultCredentialConfigValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v *vaultCredentialConfigValidator) ValidateResource(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var data VaultCredentialResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// If type is unknown/null we cannot validate yet (plan-time with unknown)
	if data.Type.IsUnknown() || data.Type.IsNull() {
		return
	}
	authType := data.Type.ValueString()

	rule, ok := credentialFieldRulesByType[authType]
	if !ok {
		return // unknown type already rejected by the OneOf validator on `type`
	}
	resp.Diagnostics.Append(validateCredentialFields(authType, rule, data)...)

	// environment_variable with networking.mode == "limited" requires non-empty
	// allowed_hosts. Skipped when an earlier check already failed.
	if authType == "environment_variable" && isSet(data.Networking) {
		var net credentialNetworkingModel
		resp.Diagnostics.Append(data.Networking.As(ctx, &net, basetypes.ObjectAsOptions{})...)
		if !resp.Diagnostics.HasError() && net.Mode.ValueString() == "limited" && (!isSet(net.AllowedHosts) || len(net.AllowedHosts.Elements()) == 0) {
			resp.Diagnostics.AddAttributeError(
				path.Root("networking").AtName("allowed_hosts"),
				"Missing required attribute",
				"\"networking.allowed_hosts\" must contain at least one entry when \"networking.mode\" is \"limited\".",
			)
		}
	}

	if authType == "environment_variable" && isSet(data.InjectionLocation) {
		resp.Diagnostics.Append(validateInjectionLocation(ctx, data.InjectionLocation)...)
	}

	resp.Diagnostics.Append(validateTokenWoVersion(data)...)
}

// isSet reports whether a write-only/optional attribute is definitively
// provided in config. An Unknown value (e.g. an unresolved var/output ref) is
// treated as neither set nor missing, so it never trips a required- or
// conflicting-attribute check.
func isSet(v attr.Value) bool { return !v.IsNull() && !v.IsUnknown() }

// isMissing reports whether a required attribute is definitively absent.
// Unknown is not missing — it may resolve to a value at apply time.
func isMissing(v attr.Value) bool { return v.IsNull() && !v.IsUnknown() }

// credentialFieldRules lists, for one auth type, the attributes that are
// required, plus the additional ones that are merely allowed. Any
// type-specific attribute in neither set is forbidden for that type. Driving
// the checks from this table keeps the three cases in lockstep instead of
// ~20 hand-written conditionals.
type credentialFieldRules struct {
	required []string
	optional []string
}

var credentialFieldRulesByType = map[string]credentialFieldRules{
	"static_bearer":        {required: []string{"token", "mcp_server_url"}},
	"mcp_oauth":            {required: []string{"access_token", "mcp_server_url"}, optional: []string{"expires_at", "refresh"}},
	"environment_variable": {required: []string{"secret_name", "secret_value", "networking"}, optional: []string{"injection_location"}},
}

// credentialFieldOrder is the stable order of the type-specific attributes,
// so emitted diagnostics are deterministic.
var credentialFieldOrder = []string{"token", "access_token", "expires_at", "refresh", "mcp_server_url", "secret_name", "secret_value", "networking", "injection_location"}

// validateCredentialFields reports each required attribute of rule that is
// missing and each attribute of another auth type that is set.
func validateCredentialFields(authType string, rule credentialFieldRules, data VaultCredentialResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics
	fieldValues := map[string]attr.Value{
		"token":              data.Token,
		"access_token":       data.AccessToken,
		"expires_at":         data.ExpiresAt,
		"refresh":            data.Refresh,
		"mcp_server_url":     data.MCPServerURL,
		"secret_name":        data.SecretName,
		"secret_value":       data.SecretValue,
		"networking":         data.Networking,
		"injection_location": data.InjectionLocation,
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

	for _, name := range credentialFieldOrder {
		switch {
		case required[name]:
			if isMissing(fieldValues[name]) {
				diags.AddAttributeError(
					path.Root(name),
					"Missing required attribute",
					fmt.Sprintf("%q is required when type is %q.", name, authType),
				)
			}
		case !allowed[name]:
			if isSet(fieldValues[name]) {
				diags.AddAttributeError(
					path.Root(name),
					"Conflicting attribute",
					fmt.Sprintf("%q must not be set when type is %q.", name, authType),
				)
			}
		}
	}
	return diags
}

// validateInjectionLocation checks that a configured injection_location keeps
// at least one location enabled (the API answers 400 otherwise). A field left
// out of a configured block defaults to false.
func validateInjectionLocation(ctx context.Context, obj types.Object) diag.Diagnostics {
	var loc credentialInjectionLocationModel
	diags := obj.As(ctx, &loc, basetypes.ObjectAsOptions{})
	isFalse := func(v types.Bool) bool { return v.IsNull() || (!v.IsUnknown() && !v.ValueBool()) }
	if !diags.HasError() && isFalse(loc.Header) && isFalse(loc.Body) {
		diags.AddAttributeError(
			path.Root("injection_location"),
			"Invalid injection location",
			"At least one of \"injection_location.header\" and \"injection_location.body\" must be true: "+
				"a credential needs one location to substitute the secret into. Fields left out default to false.",
		)
	}
	return diags
}

// validateTokenWoVersion requires token_wo_version whenever a write-only
// secret is configured. A write-only secret cannot be diffed by Terraform, so
// rotation relies on token_wo_version: bumping it is what triggers the secret
// to be re-pushed on the next apply; without it, changing the secret in
// config would be a silent no-op.
func validateTokenWoVersion(data VaultCredentialResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics
	secretConfigured := isSet(data.Token) || isSet(data.AccessToken) || isSet(data.SecretValue)
	if secretConfigured && isMissing(data.TokenWoVersion) {
		diags.AddAttributeError(
			path.Root("token_wo_version"),
			"Missing required attribute",
			"\"token_wo_version\" must be set when a write-only secret (token / access_token / secret_value) is configured. "+
				"It is the rotation trigger: Terraform cannot diff write-only values, so increment it to re-push the secret. Set it to 1 on initial creation.",
		)
	}
	return diags
}

// --- Configure ---

func (r *VaultCredentialResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

	if !providerrors.RequireResourceAPIClient(pd.Client, &resp.Diagnostics) {
		return
	}

	r.client = pd.Client
}

// --- ModifyPlan ---

// ModifyPlan keeps updated_at when the plan changes nothing else. The header
// and body fields of injection_location have a default, which the framework
// applies even when the block is not configured: it then sees a change and
// plans updated_at as unknown, and UseNonNullStateForUnknown restores the
// stored injection_location. Without this, every credential configured without
// an injection_location block (or after the block was removed) would plan an
// in-place update that changes nothing, on every plan.
//
// State written before injection_location existed holds null, which stays
// unknown in the plan (see the attribute's plan modifiers) until a refresh
// reads it. With nothing else changed, that unknown changes nothing either, so
// the stored null is kept rather than an update planned.
func (r *VaultCredentialResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.Plan.Raw.IsNull() || req.State.Raw.IsNull() {
		return
	}

	var updatedAt types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("updated_at"), &updatedAt)...)
	if resp.Diagnostics.HasError() {
		return
	}

	unchanged := resp.Plan
	resp.Diagnostics.Append(unchanged.SetAttribute(ctx, path.Root("updated_at"), updatedAt)...)
	resp.Diagnostics.Append(keepUnreadInjectionLocation(ctx, req, &unchanged)...)
	if !resp.Diagnostics.HasError() && unchanged.Raw.Equal(req.State.Raw) {
		resp.Plan = unchanged
	}
}

// keepUnreadInjectionLocation sets plan's injection_location back to the
// stored null when the plan left it unknown only because state has none and
// the block is not configured.
func keepUnreadInjectionLocation(ctx context.Context, req resource.ModifyPlanRequest, plan *tfsdk.Plan) diag.Diagnostics {
	var planned, stored types.Object
	diags := plan.GetAttribute(ctx, path.Root("injection_location"), &planned)
	diags.Append(req.State.GetAttribute(ctx, path.Root("injection_location"), &stored)...)
	if diags.HasError() || !planned.IsUnknown() || !stored.IsNull() {
		return diags
	}
	var configured types.Object
	diags.Append(req.Config.GetAttribute(ctx, path.Root("injection_location"), &configured)...)
	if diags.HasError() || !configured.IsNull() {
		return diags
	}
	diags.Append(plan.SetAttribute(ctx, path.Root("injection_location"), stored)...)
	return diags
}

// --- Create ---

func (r *VaultCredentialResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data VaultCredentialResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Read write-only secrets from config (not available in plan/state)
	auth, diags := buildCreateAuthUnion(ctx, req.Config, &data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	params := anthropic.BetaVaultCredentialNewParams{
		Auth: auth,
	}

	if !data.DisplayName.IsNull() && !data.DisplayName.IsUnknown() {
		params.DisplayName = param.NewOpt(data.DisplayName.ValueString())
	}

	if !data.Metadata.IsNull() && !data.Metadata.IsUnknown() {
		var meta map[string]string
		resp.Diagnostics.Append(data.Metadata.ElementsAs(ctx, &meta, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
		params.Metadata = meta
	}

	cred, err := r.client.Beta.Vaults.Credentials.New(ctx, data.VaultID.ValueString(), params)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create vault credential: %s", err))
		return
	}

	resp.Diagnostics.Append(mapCredentialResponseToState(ctx, cred, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Read ---

func (r *VaultCredentialResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data VaultCredentialResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	cred, err := r.client.Beta.Vaults.Credentials.Get(ctx, data.ID.ValueString(), anthropic.BetaVaultCredentialGetParams{
		VaultID: data.VaultID.ValueString(),
	})
	if err != nil {
		// The credential (or its parent vault) was deleted out-of-band: drop it
		// from state so the next plan recreates it instead of erroring forever.
		var apierr *anthropic.Error
		if errors.As(err, &apierr) && apierr.StatusCode == http.StatusNotFound {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read vault credential: %s", err))
		return
	}

	resp.Diagnostics.Append(mapCredentialResponseToState(ctx, cred, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// archive_on_destroy is local-only; default to false on import.
	if data.ArchiveOnDestroy.IsNull() || data.ArchiveOnDestroy.IsUnknown() {
		data.ArchiveOnDestroy = types.BoolValue(false)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Update ---

func (r *VaultCredentialResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan VaultCredentialResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state VaultCredentialResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	params := anthropic.BetaVaultCredentialUpdateParams{
		VaultID: plan.VaultID.ValueString(),
	}

	// display_name is mutable and nullable: send the desired value, or an
	// explicit null to clear it (omitting it would leave the old value in place).
	if plan.DisplayName.IsUnknown() {
		// Unknown should not occur for a non-computed optional attribute; leave omitted.
	} else if plan.DisplayName.IsNull() {
		params.DisplayName = param.Null[string]()
	} else {
		params.DisplayName = param.NewOpt(plan.DisplayName.ValueString())
	}

	// metadata uses PATCH semantics (omitted keys preserved, null deletes a key).
	// Build a patch that upserts planned keys and explicitly nulls keys removed
	// since the prior state, so a cleared/removed key actually converges.
	metaPatch, d := buildMetadataPatch(ctx, plan.Metadata, state.Metadata)
	resp.Diagnostics.Append(d...)
	if resp.Diagnostics.HasError() {
		return
	}
	if len(metaPatch) > 0 {
		params.SetExtraFields(map[string]any{"metadata": metaPatch})
	}

	if authUpdateNeeded(plan, state) {
		authUpdate, d := buildUpdateAuthUnion(ctx, req.Config, &plan)
		resp.Diagnostics.Append(d...)
		if resp.Diagnostics.HasError() {
			return
		}
		clearRemovedScope(&authUpdate, plan, state)
		params.Auth = authUpdate
	}

	cred, err := r.client.Beta.Vaults.Credentials.Update(ctx, state.ID.ValueString(), params)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update vault credential: %s", err))
		return
	}

	resp.Diagnostics.Append(mapCredentialResponseToState(ctx, cred, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// --- Delete ---

func (r *VaultCredentialResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data VaultCredentialResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if data.ArchiveOnDestroy.ValueBool() {
		_, err := r.client.Beta.Vaults.Credentials.Archive(ctx, data.ID.ValueString(), anthropic.BetaVaultCredentialArchiveParams{
			VaultID: data.VaultID.ValueString(),
		})
		if err != nil && !providerrors.IsNotFound(err) {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to archive vault credential: %s", err))
		}
		return
	}

	// A 404 means the credential (or its vault) is already gone, which is
	// what destroy wants.
	_, err := r.client.Beta.Vaults.Credentials.Delete(ctx, data.ID.ValueString(), anthropic.BetaVaultCredentialDeleteParams{
		VaultID: data.VaultID.ValueString(),
	})
	if err != nil && !providerrors.IsNotFound(err) {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete vault credential: %s", err))
	}
}

// --- ImportState ---

// ImportState accepts a composite ID: "<vault_id>:<credential_id>".
func (r *VaultCredentialResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.SplitN(req.ID, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			fmt.Sprintf("Expected import ID in the format \"<vault_id>:<credential_id>\", got: %q", req.ID),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("vault_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), parts[1])...)
}

// ============================================================================
// Helper functions
// ============================================================================

// authUpdateNeeded reports whether Update must re-push the auth object: on any
// change to a mutable auth field. token_wo_version covers write-only secret
// rotation (token / access_token / refresh_token / client_secret / secret_value,
// which Terraform cannot diff). The remaining mutable, non-secret auth fields are
// networking / injection_location (environment_variable) and expires_at /
// refresh (mcp_oauth); immutable auth fields are RequiresReplace and never reach
// Update. An unknown injection_location (state without one, not refreshed yet)
// sends nothing, so it is not a change.
func authUpdateNeeded(plan, state VaultCredentialResourceModel) bool {
	if !plan.TokenWoVersion.Equal(state.TokenWoVersion) {
		return true
	}
	switch plan.Type.ValueString() {
	case "environment_variable":
		locationChanged := !plan.InjectionLocation.IsUnknown() && !plan.InjectionLocation.Equal(state.InjectionLocation)
		return !plan.Networking.Equal(state.Networking) || locationChanged
	case "mcp_oauth":
		return !plan.ExpiresAt.Equal(state.ExpiresAt) || !plan.Refresh.Equal(state.Refresh)
	}
	return false
}

// clearRemovedScope sends refresh.scope as "" when the plan removes a stored
// scope: the update keeps an omitted scope and clears it on an empty string
// ("Send an empty string to clear the stored scope",
// https://platform.claude.com/docs/en/api/beta/vaults/credentials/update).
func clearRemovedScope(auth *anthropic.BetaVaultCredentialUpdateParamsAuthUnion, plan, state VaultCredentialResourceModel) {
	if auth.OfMCPOAuth == nil || plan.Refresh.IsNull() || plan.Refresh.IsUnknown() || state.Refresh.IsNull() || state.Refresh.IsUnknown() {
		return
	}
	planScope, _ := plan.Refresh.Attributes()["scope"].(types.String)
	stateScope, _ := state.Refresh.Attributes()["scope"].(types.String)
	if planScope.IsNull() && !stateScope.IsNull() && !stateScope.IsUnknown() {
		auth.OfMCPOAuth.Refresh.Scope = param.NewOpt("")
	}
}

// buildCreateAuthUnion builds the BetaVaultCredentialNewParamsAuthUnion from plan + config (write-only).
// Write-only values are read from cfg (tfsdk.Config) because they are absent from plan/state.
func buildCreateAuthUnion(ctx context.Context, cfg tfsdk.Config, data *VaultCredentialResourceModel) (anthropic.BetaVaultCredentialNewParamsAuthUnion, diag.Diagnostics) {
	switch data.Type.ValueString() {
	case "static_bearer":
		return buildStaticBearerCreateAuth(ctx, cfg, data)
	case "mcp_oauth":
		return buildMCPOAuthCreateAuth(ctx, cfg, data)
	case "environment_variable":
		return buildEnvironmentVariableCreateAuth(ctx, cfg, data)
	}

	var diags diag.Diagnostics
	diags.AddError("Internal Error", fmt.Sprintf("Unknown credential type: %s", data.Type.ValueString()))
	return anthropic.BetaVaultCredentialNewParamsAuthUnion{}, diags
}

func buildStaticBearerCreateAuth(ctx context.Context, cfg tfsdk.Config, data *VaultCredentialResourceModel) (anthropic.BetaVaultCredentialNewParamsAuthUnion, diag.Diagnostics) {
	token, diags := readSecret(ctx, cfg, path.Root("token"))
	if diags.HasError() {
		return anthropic.BetaVaultCredentialNewParamsAuthUnion{}, diags
	}
	return anthropic.BetaVaultCredentialNewParamsAuthUnion{
		OfStaticBearer: &anthropic.BetaManagedAgentsStaticBearerCreateParams{
			Token:        token.ValueString(),
			MCPServerURL: data.MCPServerURL.ValueString(),
			Type:         "static_bearer",
		},
	}, diags
}

func buildMCPOAuthCreateAuth(ctx context.Context, cfg tfsdk.Config, data *VaultCredentialResourceModel) (anthropic.BetaVaultCredentialNewParamsAuthUnion, diag.Diagnostics) {
	accessToken, diags := readSecret(ctx, cfg, path.Root("access_token"))
	if diags.HasError() {
		return anthropic.BetaVaultCredentialNewParamsAuthUnion{}, diags
	}

	oauthParams := &anthropic.BetaManagedAgentsMCPOAuthCreateParams{
		AccessToken:  accessToken.ValueString(),
		MCPServerURL: data.MCPServerURL.ValueString(),
		Type:         "mcp_oauth",
	}

	expiresAt, d := parseExpiresAt(data.ExpiresAt)
	diags.Append(d...)
	if diags.HasError() {
		return anthropic.BetaVaultCredentialNewParamsAuthUnion{}, diags
	}
	oauthParams.ExpiresAt = expiresAt

	if isSet(data.Refresh) {
		refreshParams, d := buildRefreshParams(ctx, cfg, data.Refresh)
		diags.Append(d...)
		if diags.HasError() {
			return anthropic.BetaVaultCredentialNewParamsAuthUnion{}, diags
		}
		oauthParams.Refresh = refreshParams
	}

	return anthropic.BetaVaultCredentialNewParamsAuthUnion{OfMCPOAuth: oauthParams}, diags
}

func buildEnvironmentVariableCreateAuth(ctx context.Context, cfg tfsdk.Config, data *VaultCredentialResourceModel) (anthropic.BetaVaultCredentialNewParamsAuthUnion, diag.Diagnostics) {
	secretValue, diags := readSecret(ctx, cfg, path.Root("secret_value"))
	if diags.HasError() {
		return anthropic.BetaVaultCredentialNewParamsAuthUnion{}, diags
	}

	networking, d := buildNetworkingUnion(ctx, data.Networking)
	diags.Append(d...)
	if diags.HasError() {
		return anthropic.BetaVaultCredentialNewParamsAuthUnion{}, diags
	}

	envParams := &anthropic.BetaManagedAgentsEnvironmentVariableCreateParams{
		SecretName:  data.SecretName.ValueString(),
		SecretValue: secretValue.ValueString(),
		Networking:  networking,
		Type:        "environment_variable",
	}
	// Omitted when unknown (no block in config): the API then enables both
	// locations, and the response fills the Computed value.
	header, body, ok, d := injectionLocationFromPlan(ctx, data.InjectionLocation)
	diags.Append(d...)
	if diags.HasError() {
		return anthropic.BetaVaultCredentialNewParamsAuthUnion{}, diags
	}
	if ok {
		envParams.InjectionLocation = anthropic.BetaManagedAgentsInjectionLocationParams{
			Header: param.NewOpt(header),
			Body:   param.NewOpt(body),
		}
	}

	return anthropic.BetaVaultCredentialNewParamsAuthUnion{OfEnvironmentVariable: envParams}, diags
}

// buildUpdateAuthUnion builds the BetaVaultCredentialUpdateParamsAuthUnion for updates.
func buildUpdateAuthUnion(ctx context.Context, cfg tfsdk.Config, data *VaultCredentialResourceModel) (anthropic.BetaVaultCredentialUpdateParamsAuthUnion, diag.Diagnostics) {
	switch data.Type.ValueString() {
	case "static_bearer":
		return buildStaticBearerUpdateAuth(ctx, cfg)
	case "mcp_oauth":
		return buildMCPOAuthUpdateAuth(ctx, cfg, data)
	case "environment_variable":
		return buildEnvironmentVariableUpdateAuth(ctx, cfg, data)
	}

	var diags diag.Diagnostics
	diags.AddError("Internal Error", fmt.Sprintf("Unknown credential type: %s", data.Type.ValueString()))
	return anthropic.BetaVaultCredentialUpdateParamsAuthUnion{}, diags
}

func buildStaticBearerUpdateAuth(ctx context.Context, cfg tfsdk.Config) (anthropic.BetaVaultCredentialUpdateParamsAuthUnion, diag.Diagnostics) {
	token, diags := readSecret(ctx, cfg, path.Root("token"))
	if diags.HasError() {
		return anthropic.BetaVaultCredentialUpdateParamsAuthUnion{}, diags
	}
	return anthropic.BetaVaultCredentialUpdateParamsAuthUnion{OfStaticBearer: &anthropic.BetaManagedAgentsStaticBearerUpdateParams{
		Type:  "static_bearer",
		Token: nonEmptySecret(token),
	}}, diags
}

func buildMCPOAuthUpdateAuth(ctx context.Context, cfg tfsdk.Config, data *VaultCredentialResourceModel) (anthropic.BetaVaultCredentialUpdateParamsAuthUnion, diag.Diagnostics) {
	accessToken, diags := readSecret(ctx, cfg, path.Root("access_token"))
	if diags.HasError() {
		return anthropic.BetaVaultCredentialUpdateParamsAuthUnion{}, diags
	}

	oauthUpdate := &anthropic.BetaManagedAgentsMCPOAuthUpdateParams{
		Type:        "mcp_oauth",
		AccessToken: nonEmptySecret(accessToken),
	}

	expiresAt, d := parseExpiresAt(data.ExpiresAt)
	diags.Append(d...)
	if diags.HasError() {
		return anthropic.BetaVaultCredentialUpdateParamsAuthUnion{}, diags
	}
	oauthUpdate.ExpiresAt = expiresAt

	if isSet(data.Refresh) {
		refreshUpdate, d := buildRefreshUpdateParams(ctx, cfg, data.Refresh)
		diags.Append(d...)
		if diags.HasError() {
			return anthropic.BetaVaultCredentialUpdateParamsAuthUnion{}, diags
		}
		oauthUpdate.Refresh = refreshUpdate
	}

	return anthropic.BetaVaultCredentialUpdateParamsAuthUnion{OfMCPOAuth: oauthUpdate}, diags
}

func buildEnvironmentVariableUpdateAuth(ctx context.Context, cfg tfsdk.Config, data *VaultCredentialResourceModel) (anthropic.BetaVaultCredentialUpdateParamsAuthUnion, diag.Diagnostics) {
	secretValue, diags := readSecret(ctx, cfg, path.Root("secret_value"))
	if diags.HasError() {
		return anthropic.BetaVaultCredentialUpdateParamsAuthUnion{}, diags
	}

	envUpdate := &anthropic.BetaManagedAgentsEnvironmentVariableUpdateParams{
		Type:        "environment_variable",
		SecretValue: nonEmptySecret(secretValue),
	}

	if isSet(data.Networking) {
		networking, d := buildNetworkingUnion(ctx, data.Networking)
		diags.Append(d...)
		if diags.HasError() {
			return anthropic.BetaVaultCredentialUpdateParamsAuthUnion{}, diags
		}
		envUpdate.Networking = networking
	}

	// The update merges injection_location per field; both are sent so the
	// stored value converges on the plan whatever it was before.
	header, body, ok, d := injectionLocationFromPlan(ctx, data.InjectionLocation)
	diags.Append(d...)
	if diags.HasError() {
		return anthropic.BetaVaultCredentialUpdateParamsAuthUnion{}, diags
	}
	if ok {
		envUpdate.InjectionLocation = anthropic.BetaManagedAgentsInjectionLocationUpdateParams{
			Header: param.NewOpt(header),
			Body:   param.NewOpt(body),
		}
	}

	return anthropic.BetaVaultCredentialUpdateParamsAuthUnion{OfEnvironmentVariable: envUpdate}, diags
}

// readSecret reads the write-only attribute at p from the configuration, the
// only place a write-only value is available.
func readSecret(ctx context.Context, cfg tfsdk.Config, p path.Path) (types.String, diag.Diagnostics) {
	var v types.String
	diags := cfg.GetAttribute(ctx, p, &v)
	return v, diags
}

// nonEmptySecret is the update value of a secret: a known, non-empty secret
// replaces the stored one; an empty, null or unknown one is left out, which
// keeps it.
func nonEmptySecret(v types.String) param.Opt[string] {
	if !isSet(v) || v.ValueString() == "" {
		return param.Opt[string]{}
	}
	return param.NewOpt(v.ValueString())
}

// parseExpiresAt parses a known expires_at as RFC 3339; a null or unknown one
// is left out.
func parseExpiresAt(v types.String) (param.Opt[time.Time], diag.Diagnostics) {
	var diags diag.Diagnostics
	if !isSet(v) {
		return param.Opt[time.Time]{}, diags
	}
	t, err := time.Parse(time.RFC3339, v.ValueString())
	if err != nil {
		diags.AddAttributeError(path.Root("expires_at"), "Invalid timestamp", fmt.Sprintf("Cannot parse expires_at as RFC3339: %s", err))
		return param.Opt[time.Time]{}, diags
	}
	return param.NewOpt(t), diags
}

// buildRefreshParams builds BetaManagedAgentsMCPOAuthRefreshParams reading write-only
// refresh_token and client_secret from the raw tfsdk.Config.
func buildRefreshParams(ctx context.Context, cfg tfsdk.Config, refreshObj types.Object) (anthropic.BetaManagedAgentsMCPOAuthRefreshParams, diag.Diagnostics) {
	var diags diag.Diagnostics
	var refreshModel credentialRefreshModel
	diags.Append(refreshObj.As(ctx, &refreshModel, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return anthropic.BetaManagedAgentsMCPOAuthRefreshParams{}, diags
	}

	// Read write-only refresh_token from config
	var refreshToken types.String
	diags.Append(cfg.GetAttribute(ctx, path.Root("refresh").AtName("refresh_token"), &refreshToken)...)
	if diags.HasError() {
		return anthropic.BetaManagedAgentsMCPOAuthRefreshParams{}, diags
	}

	// Build token_endpoint_auth
	var teaModel credentialTokenEndpointAuthModel
	diags.Append(refreshModel.TokenEndpointAuth.As(ctx, &teaModel, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return anthropic.BetaManagedAgentsMCPOAuthRefreshParams{}, diags
	}

	teaUnion, d := buildTokenEndpointAuthUnion(ctx, cfg, teaModel)
	diags.Append(d...)
	if diags.HasError() {
		return anthropic.BetaManagedAgentsMCPOAuthRefreshParams{}, diags
	}

	refreshParams := anthropic.BetaManagedAgentsMCPOAuthRefreshParams{
		ClientID:          refreshModel.ClientID.ValueString(),
		RefreshToken:      refreshToken.ValueString(),
		TokenEndpoint:     refreshModel.TokenEndpoint.ValueString(),
		TokenEndpointAuth: teaUnion,
	}

	if !refreshModel.Resource.IsNull() && !refreshModel.Resource.IsUnknown() {
		refreshParams.Resource = param.NewOpt(refreshModel.Resource.ValueString())
	}
	if !refreshModel.Scope.IsNull() && !refreshModel.Scope.IsUnknown() {
		refreshParams.Scope = param.NewOpt(refreshModel.Scope.ValueString())
	}

	return refreshParams, diags
}

// buildRefreshUpdateParams builds BetaManagedAgentsMCPOAuthRefreshUpdateParams for updates.
func buildRefreshUpdateParams(ctx context.Context, cfg tfsdk.Config, refreshObj types.Object) (anthropic.BetaManagedAgentsMCPOAuthRefreshUpdateParams, diag.Diagnostics) {
	var diags diag.Diagnostics
	var refreshModel credentialRefreshModel
	diags.Append(refreshObj.As(ctx, &refreshModel, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return anthropic.BetaManagedAgentsMCPOAuthRefreshUpdateParams{}, diags
	}

	refreshToken, d := readSecret(ctx, cfg, path.Root("refresh").AtName("refresh_token"))
	diags.Append(d...)
	if diags.HasError() {
		return anthropic.BetaManagedAgentsMCPOAuthRefreshUpdateParams{}, diags
	}

	updateParams := anthropic.BetaManagedAgentsMCPOAuthRefreshUpdateParams{
		RefreshToken: nonEmptySecret(refreshToken),
	}
	if isSet(refreshModel.Scope) {
		updateParams.Scope = param.NewOpt(refreshModel.Scope.ValueString())
	}

	if isSet(refreshModel.TokenEndpointAuth) {
		tea, d := buildTokenEndpointAuthUpdate(ctx, cfg, refreshModel.TokenEndpointAuth)
		diags.Append(d...)
		if diags.HasError() {
			return anthropic.BetaManagedAgentsMCPOAuthRefreshUpdateParams{}, diags
		}
		updateParams.TokenEndpointAuth = tea
	}

	return updateParams, diags
}

// buildTokenEndpointAuthUpdate builds the token endpoint auth union for
// updates. Only client_secret_basic/client_secret_post are re-sent on update
// (e.g. to rotate the client secret). "none" has no update-union variant in
// the SDK and is never transmitted here; a change of token_endpoint_auth.type
// is RequiresReplace, so a transition to/from "none" recreates the resource
// rather than reaching this update path.
func buildTokenEndpointAuthUpdate(ctx context.Context, cfg tfsdk.Config, obj types.Object) (anthropic.BetaManagedAgentsMCPOAuthRefreshUpdateParamsTokenEndpointAuthUnion, diag.Diagnostics) {
	var teaModel credentialTokenEndpointAuthModel
	diags := obj.As(ctx, &teaModel, basetypes.ObjectAsOptions{})
	if diags.HasError() {
		return anthropic.BetaManagedAgentsMCPOAuthRefreshUpdateParamsTokenEndpointAuthUnion{}, diags
	}

	teaType := teaModel.Type.ValueString()
	if teaType != "client_secret_basic" && teaType != "client_secret_post" {
		return anthropic.BetaManagedAgentsMCPOAuthRefreshUpdateParamsTokenEndpointAuthUnion{}, diags
	}

	cs, d := readSecret(ctx, cfg, path.Root("refresh").AtName("token_endpoint_auth").AtName("client_secret"))
	diags.Append(d...)
	if diags.HasError() {
		return anthropic.BetaManagedAgentsMCPOAuthRefreshUpdateParamsTokenEndpointAuthUnion{}, diags
	}
	if teaType == "client_secret_basic" {
		return anthropic.BetaManagedAgentsMCPOAuthRefreshUpdateParamsTokenEndpointAuthUnion{
			OfClientSecretBasic: &anthropic.BetaManagedAgentsTokenEndpointAuthBasicUpdateParam{
				Type:         "client_secret_basic",
				ClientSecret: nonEmptySecret(cs),
			},
		}, diags
	}
	return anthropic.BetaManagedAgentsMCPOAuthRefreshUpdateParamsTokenEndpointAuthUnion{
		OfClientSecretPost: &anthropic.BetaManagedAgentsTokenEndpointAuthPostUpdateParam{
			Type:         "client_secret_post",
			ClientSecret: nonEmptySecret(cs),
		},
	}, diags
}

// buildTokenEndpointAuthUnion builds the token endpoint auth union for Create.
func buildTokenEndpointAuthUnion(ctx context.Context, cfg tfsdk.Config, teaModel credentialTokenEndpointAuthModel) (anthropic.BetaManagedAgentsMCPOAuthRefreshParamsTokenEndpointAuthUnion, diag.Diagnostics) {
	var diags diag.Diagnostics

	switch teaModel.Type.ValueString() {
	case "none":
		return anthropic.BetaManagedAgentsMCPOAuthRefreshParamsTokenEndpointAuthUnion{
			OfNone: &anthropic.BetaManagedAgentsTokenEndpointAuthNoneParam{Type: "none"},
		}, diags

	case "client_secret_basic":
		var cs types.String
		diags.Append(cfg.GetAttribute(ctx, path.Root("refresh").AtName("token_endpoint_auth").AtName("client_secret"), &cs)...)
		if diags.HasError() {
			return anthropic.BetaManagedAgentsMCPOAuthRefreshParamsTokenEndpointAuthUnion{}, diags
		}
		return anthropic.BetaManagedAgentsMCPOAuthRefreshParamsTokenEndpointAuthUnion{
			OfClientSecretBasic: &anthropic.BetaManagedAgentsTokenEndpointAuthBasicParam{
				ClientSecret: cs.ValueString(),
				Type:         "client_secret_basic",
			},
		}, diags

	case "client_secret_post":
		var cs types.String
		diags.Append(cfg.GetAttribute(ctx, path.Root("refresh").AtName("token_endpoint_auth").AtName("client_secret"), &cs)...)
		if diags.HasError() {
			return anthropic.BetaManagedAgentsMCPOAuthRefreshParamsTokenEndpointAuthUnion{}, diags
		}
		return anthropic.BetaManagedAgentsMCPOAuthRefreshParamsTokenEndpointAuthUnion{
			OfClientSecretPost: &anthropic.BetaManagedAgentsTokenEndpointAuthPostParam{
				ClientSecret: cs.ValueString(),
				Type:         "client_secret_post",
			},
		}, diags
	}

	diags.AddError("Internal Error", fmt.Sprintf("Unknown token_endpoint_auth type: %s", teaModel.Type.ValueString()))
	return anthropic.BetaManagedAgentsMCPOAuthRefreshParamsTokenEndpointAuthUnion{}, diags
}

// buildNetworkingUnion builds BetaManagedAgentsCredentialNetworkingParamsUnion from a types.Object.
func buildNetworkingUnion(ctx context.Context, networkingObj types.Object) (anthropic.BetaManagedAgentsCredentialNetworkingParamsUnion, diag.Diagnostics) {
	var diags diag.Diagnostics
	var net credentialNetworkingModel
	diags.Append(networkingObj.As(ctx, &net, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return anthropic.BetaManagedAgentsCredentialNetworkingParamsUnion{}, diags
	}

	if net.Mode.ValueString() == "unrestricted" {
		return anthropic.BetaManagedAgentsCredentialNetworkingParamsOfUnrestricted("unrestricted"), diags
	}

	// limited
	var hosts []string
	if !net.AllowedHosts.IsNull() && !net.AllowedHosts.IsUnknown() {
		diags.Append(net.AllowedHosts.ElementsAs(ctx, &hosts, false)...)
		if diags.HasError() {
			return anthropic.BetaManagedAgentsCredentialNetworkingParamsUnion{}, diags
		}
	}
	// NOTE: the SDK's BetaManagedAgentsCredentialNetworkingParamsOfLimited helper
	// does not set the "type" discriminator, so the API rejects the request with
	// "networking.type: Field required". Construct the variant explicitly instead.
	return anthropic.BetaManagedAgentsCredentialNetworkingParamsUnion{
		OfLimited: &anthropic.BetaManagedAgentsLimitedCredentialNetworkingParams{
			Type:         "limited",
			AllowedHosts: hosts,
		},
	}, diags
}

// injectionLocationFromPlan returns the planned injection_location fields, with
// ok false when the object or one of its fields is null or unknown (nothing to
// send: the API keeps or resolves the value and the response fills it in).
func injectionLocationFromPlan(ctx context.Context, obj types.Object) (header, body, ok bool, diags diag.Diagnostics) {
	if obj.IsNull() || obj.IsUnknown() {
		return false, false, false, diags
	}
	var loc credentialInjectionLocationModel
	diags.Append(obj.As(ctx, &loc, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return false, false, false, diags
	}
	for _, v := range []types.Bool{loc.Header, loc.Body} {
		if v.IsNull() || v.IsUnknown() {
			return false, false, false, diags
		}
	}
	return loc.Header.ValueBool(), loc.Body.ValueBool(), true, diags
}

// mapInjectionLocationResponseToObject maps the resolved injection_location of
// an environment_variable credential. The API always returns both fields; an
// absent object maps to null.
func mapInjectionLocationResponseToObject(auth anthropic.BetaManagedAgentsCredentialAuthUnion) (types.Object, diag.Diagnostics) {
	if !auth.JSON.InjectionLocation.Valid() {
		return types.ObjectNull(credentialInjectionLocationAttrTypes), nil
	}
	return types.ObjectValue(credentialInjectionLocationAttrTypes, map[string]attr.Value{
		"header": types.BoolValue(auth.InjectionLocation.Header),
		"body":   types.BoolValue(auth.InjectionLocation.Body),
	})
}

// mcpServerURLForState returns the user-provided URL when it is equivalent to
// the API value modulo a trailing slash. The API normalizes a bare host by
// appending "/" (e.g. "https://mcp.example.com" -> "https://mcp.example.com/").
// Since mcp_server_url is RequiresReplace (not Computed), blindly storing the
// API value would cause "Provider produced inconsistent result after apply" and
// a perpetual replace diff. Preserving the equivalent user value avoids both; a
// genuinely different URL still falls through to the API value.
func mcpServerURLForState(planned types.String, apiVal string) types.String {
	if !planned.IsNull() && !planned.IsUnknown() &&
		strings.TrimRight(planned.ValueString(), "/") == strings.TrimRight(apiVal, "/") {
		return planned
	}
	return types.StringValue(apiVal)
}

// mapCredentialResponseToState maps non-secret API response fields to Terraform state.
// Write-only fields (token, access_token, refresh_token, client_secret, secret_value) are NEVER set here.
func mapCredentialResponseToState(ctx context.Context, cred *anthropic.BetaManagedAgentsCredential, data *VaultCredentialResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics

	data.ID = types.StringValue(cred.ID)
	data.VaultID = types.StringValue(cred.VaultID)
	data.CredentialType = types.StringValue(string(cred.Type))

	if cred.DisplayName != "" {
		data.DisplayName = types.StringValue(cred.DisplayName)
	} else if !data.DisplayName.IsNull() {
		data.DisplayName = types.StringNull()
	}

	// Timestamps
	if !cred.CreatedAt.IsZero() {
		data.CreatedAt = types.StringValue(cred.CreatedAt.Format(time.RFC3339))
	}
	if !cred.UpdatedAt.IsZero() {
		data.UpdatedAt = types.StringValue(cred.UpdatedAt.Format(time.RFC3339))
	}
	if cred.ArchivedAt.IsZero() {
		data.ArchivedAt = types.StringNull()
	} else {
		data.ArchivedAt = types.StringValue(cred.ArchivedAt.Format(time.RFC3339))
	}

	// Metadata
	if len(cred.Metadata) > 0 {
		elements := make(map[string]attr.Value, len(cred.Metadata))
		for k, v := range cred.Metadata {
			elements[k] = types.StringValue(v)
		}
		metaMap, d := types.MapValue(types.StringType, elements)
		diags.Append(d...)
		data.Metadata = metaMap
	} else {
		// The API answers {} both without metadata and after metadata = {}.
		data.Metadata = tfvalue.MetadataAfterEmptyResponse(data.Metadata)
	}

	// Map auth response back into non-secret state fields.
	// The union struct exposes all variant fields as flat fields (populated by JSON unmarshaling).
	// Secret fields (token, access_token, refresh_token, client_secret, secret_value) are NEVER set.
	authType := cred.Auth.Type
	data.Type = types.StringValue(authType)

	switch authType {
	case "static_bearer":
		// MCPServerURL is a flat field on the union struct.
		data.MCPServerURL = mcpServerURLForState(data.MCPServerURL, cred.Auth.MCPServerURL)
		// Clear oauth/env-var fields
		data.ExpiresAt = types.StringNull()
		data.Refresh = types.ObjectNull(credentialRefreshAttrTypes)
		data.SecretName = types.StringNull()
		data.Networking = types.ObjectNull(credentialNetworkingAttrTypes)
		data.InjectionLocation = types.ObjectNull(credentialInjectionLocationAttrTypes)

	case "mcp_oauth":
		data.MCPServerURL = mcpServerURLForState(data.MCPServerURL, cred.Auth.MCPServerURL)
		if cred.Auth.ExpiresAt.IsZero() {
			data.ExpiresAt = types.StringNull()
		} else {
			data.ExpiresAt = types.StringValue(cred.Auth.ExpiresAt.Format(time.RFC3339))
		}
		// Map refresh (non-secret fields only) — Refresh is a flat field on the union struct.
		if cred.Auth.JSON.Refresh.Valid() {
			refreshObj, d := mapRefreshResponseToObject(cred.Auth.Refresh)
			diags.Append(d...)
			data.Refresh = refreshObj
		} else {
			data.Refresh = types.ObjectNull(credentialRefreshAttrTypes)
		}
		// Clear env-var fields
		data.SecretName = types.StringNull()
		data.Networking = types.ObjectNull(credentialNetworkingAttrTypes)
		data.InjectionLocation = types.ObjectNull(credentialInjectionLocationAttrTypes)

	case "environment_variable":
		// SecretName and Networking are flat fields on the union struct.
		data.SecretName = types.StringValue(cred.Auth.SecretName)
		// Map networking
		networkingObj, d := mapNetworkingResponseToObject(ctx, cred.Auth.Networking)
		diags.Append(d...)
		data.Networking = networkingObj
		injectionLocationObj, d := mapInjectionLocationResponseToObject(cred.Auth)
		diags.Append(d...)
		data.InjectionLocation = injectionLocationObj
		// Clear oauth/bearer fields
		data.MCPServerURL = types.StringNull()
		data.ExpiresAt = types.StringNull()
		data.Refresh = types.ObjectNull(credentialRefreshAttrTypes)
	}

	return diags
}

// mapRefreshResponseToObject maps a BetaManagedAgentsMCPOAuthRefreshResponse to a types.Object.
// refresh_token is write-only and client_secret is never in the response, so both are null.
func mapRefreshResponseToObject(refresh anthropic.BetaManagedAgentsMCPOAuthRefreshResponse) (types.Object, diag.Diagnostics) {
	var diags diag.Diagnostics

	teaType := refresh.TokenEndpointAuth.Type
	teaObj, d := types.ObjectValue(credentialTokenEndpointAuthAttrTypes, map[string]attr.Value{
		"type":          types.StringValue(teaType),
		"client_secret": types.StringNull(), // write-only; never in response
	})
	diags.Append(d...)
	if diags.HasError() {
		return types.ObjectNull(credentialRefreshAttrTypes), diags
	}

	resourceVal := types.StringNull()
	if refresh.Resource != "" {
		resourceVal = types.StringValue(refresh.Resource)
	}
	scopeVal := types.StringNull()
	if refresh.Scope != "" {
		scopeVal = types.StringValue(refresh.Scope)
	}

	refreshObj, d := types.ObjectValue(credentialRefreshAttrTypes, map[string]attr.Value{
		"client_id":           types.StringValue(refresh.ClientID),
		"refresh_token":       types.StringNull(), // write-only; never in response
		"token_endpoint":      types.StringValue(refresh.TokenEndpoint),
		"resource":            resourceVal,
		"scope":               scopeVal,
		"token_endpoint_auth": teaObj,
	})
	diags.Append(d...)
	return refreshObj, diags
}

// mapNetworkingResponseToObject maps environment_variable networking response to types.Object.
func mapNetworkingResponseToObject(ctx context.Context, networking anthropic.BetaManagedAgentsEnvironmentVariableAuthResponseNetworkingUnion) (types.Object, diag.Diagnostics) {
	var diags diag.Diagnostics

	mode := networking.Type
	var allowedHostsVal types.List

	if mode == "limited" {
		lim := networking.AsLimited()
		var d diag.Diagnostics
		allowedHostsVal, d = types.ListValueFrom(ctx, types.StringType, lim.AllowedHosts)
		diags.Append(d...)
	} else {
		allowedHostsVal = types.ListNull(types.StringType)
	}

	obj, d := types.ObjectValue(credentialNetworkingAttrTypes, map[string]attr.Value{
		"mode":          types.StringValue(mode),
		"allowed_hosts": allowedHostsVal,
	})
	diags.Append(d...)
	return obj, diags
}
