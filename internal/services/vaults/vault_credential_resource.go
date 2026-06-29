// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package vaults

import (
	"context"
	"errors"
	"fmt"
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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &VaultCredentialResource{}
var _ resource.ResourceWithImportState = &VaultCredentialResource{}
var _ resource.ResourceWithConfigValidators = &VaultCredentialResource{}

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
	SecretName  types.String `tfsdk:"secret_name"`
	SecretValue types.String `tfsdk:"secret_value"`
	Networking  types.Object `tfsdk:"networking"`

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

// --- Attribute type maps for nested objects ---

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
								MarkdownDescription: "Authentication method type. One of `none`, `client_secret_basic`, `client_secret_post`.",
								Validators:          []validator.String{stringvalidator.OneOf("none", "client_secret_basic", "client_secret_post")},
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

func (v *vaultCredentialConfigValidator) MarkdownDescription(_ context.Context) string {
	return v.Description(context.Background())
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

	// isSet reports whether a write-only/optional attribute is definitively
	// provided in config. An Unknown value (e.g. an unresolved var/output ref)
	// is treated as neither set nor missing, so it never trips a required- or
	// conflicting-attribute check.
	isSet := func(v attr.Value) bool { return !v.IsNull() && !v.IsUnknown() }
	// isMissing reports whether a required attribute is definitively absent.
	// Unknown is not missing — it may resolve to a value at apply time.
	isMissing := func(v attr.Value) bool { return v.IsNull() && !v.IsUnknown() }

	switch authType {
	case "static_bearer":
		// Require token + mcp_server_url; forbid oauth/env-var attrs
		if isMissing(data.Token) {
			resp.Diagnostics.AddAttributeError(
				path.Root("token"),
				"Missing required attribute",
				"\"token\" is required when type is \"static_bearer\".",
			)
		}
		if isMissing(data.MCPServerURL) {
			resp.Diagnostics.AddAttributeError(
				path.Root("mcp_server_url"),
				"Missing required attribute",
				"\"mcp_server_url\" is required when type is \"static_bearer\".",
			)
		}
		if isSet(data.AccessToken) {
			resp.Diagnostics.AddAttributeError(path.Root("access_token"), "Conflicting attribute", "\"access_token\" must not be set when type is \"static_bearer\".")
		}
		if isSet(data.ExpiresAt) {
			resp.Diagnostics.AddAttributeError(path.Root("expires_at"), "Conflicting attribute", "\"expires_at\" must not be set when type is \"static_bearer\".")
		}
		if isSet(data.Refresh) {
			resp.Diagnostics.AddAttributeError(path.Root("refresh"), "Conflicting attribute", "\"refresh\" must not be set when type is \"static_bearer\".")
		}
		if isSet(data.SecretName) {
			resp.Diagnostics.AddAttributeError(path.Root("secret_name"), "Conflicting attribute", "\"secret_name\" must not be set when type is \"static_bearer\".")
		}
		if isSet(data.SecretValue) {
			resp.Diagnostics.AddAttributeError(path.Root("secret_value"), "Conflicting attribute", "\"secret_value\" must not be set when type is \"static_bearer\".")
		}
		if isSet(data.Networking) {
			resp.Diagnostics.AddAttributeError(path.Root("networking"), "Conflicting attribute", "\"networking\" must not be set when type is \"static_bearer\".")
		}

	case "mcp_oauth":
		// Require access_token + mcp_server_url; forbid static_bearer/env-var attrs
		if isMissing(data.AccessToken) {
			resp.Diagnostics.AddAttributeError(
				path.Root("access_token"),
				"Missing required attribute",
				"\"access_token\" is required when type is \"mcp_oauth\".",
			)
		}
		if isMissing(data.MCPServerURL) {
			resp.Diagnostics.AddAttributeError(
				path.Root("mcp_server_url"),
				"Missing required attribute",
				"\"mcp_server_url\" is required when type is \"mcp_oauth\".",
			)
		}
		if isSet(data.Token) {
			resp.Diagnostics.AddAttributeError(path.Root("token"), "Conflicting attribute", "\"token\" must not be set when type is \"mcp_oauth\".")
		}
		if isSet(data.SecretName) {
			resp.Diagnostics.AddAttributeError(path.Root("secret_name"), "Conflicting attribute", "\"secret_name\" must not be set when type is \"mcp_oauth\".")
		}
		if isSet(data.SecretValue) {
			resp.Diagnostics.AddAttributeError(path.Root("secret_value"), "Conflicting attribute", "\"secret_value\" must not be set when type is \"mcp_oauth\".")
		}
		if isSet(data.Networking) {
			resp.Diagnostics.AddAttributeError(path.Root("networking"), "Conflicting attribute", "\"networking\" must not be set when type is \"mcp_oauth\".")
		}

	case "environment_variable":
		// Require secret_name + secret_value + networking; forbid mcp_server_url/oauth/bearer attrs
		if isMissing(data.SecretName) {
			resp.Diagnostics.AddAttributeError(
				path.Root("secret_name"),
				"Missing required attribute",
				"\"secret_name\" is required when type is \"environment_variable\".",
			)
		}
		if isMissing(data.SecretValue) {
			resp.Diagnostics.AddAttributeError(
				path.Root("secret_value"),
				"Missing required attribute",
				"\"secret_value\" is required when type is \"environment_variable\".",
			)
		}
		if isMissing(data.Networking) {
			resp.Diagnostics.AddAttributeError(
				path.Root("networking"),
				"Missing required attribute",
				"\"networking\" is required when type is \"environment_variable\".",
			)
		}
		if isSet(data.MCPServerURL) {
			resp.Diagnostics.AddAttributeError(path.Root("mcp_server_url"), "Conflicting attribute", "\"mcp_server_url\" must not be set when type is \"environment_variable\".")
		}
		if isSet(data.Token) {
			resp.Diagnostics.AddAttributeError(path.Root("token"), "Conflicting attribute", "\"token\" must not be set when type is \"environment_variable\".")
		}
		if isSet(data.AccessToken) {
			resp.Diagnostics.AddAttributeError(path.Root("access_token"), "Conflicting attribute", "\"access_token\" must not be set when type is \"environment_variable\".")
		}
		if isSet(data.ExpiresAt) {
			resp.Diagnostics.AddAttributeError(path.Root("expires_at"), "Conflicting attribute", "\"expires_at\" must not be set when type is \"environment_variable\".")
		}
		if isSet(data.Refresh) {
			resp.Diagnostics.AddAttributeError(path.Root("refresh"), "Conflicting attribute", "\"refresh\" must not be set when type is \"environment_variable\".")
		}

		// When networking.mode == "limited", require non-empty allowed_hosts
		if !data.Networking.IsNull() && !data.Networking.IsUnknown() {
			var net credentialNetworkingModel
			diags := data.Networking.As(ctx, &net, basetypes.ObjectAsOptions{})
			resp.Diagnostics.Append(diags...)
			if !resp.Diagnostics.HasError() && net.Mode.ValueString() == "limited" {
				if net.AllowedHosts.IsNull() || net.AllowedHosts.IsUnknown() || len(net.AllowedHosts.Elements()) == 0 {
					resp.Diagnostics.AddAttributeError(
						path.Root("networking").AtName("allowed_hosts"),
						"Missing required attribute",
						"\"networking.allowed_hosts\" must contain at least one entry when \"networking.mode\" is \"limited\".",
					)
				}
			}
		}
	}

	// A write-only secret cannot be diffed by Terraform, so rotation relies on
	// token_wo_version: bumping it is what triggers the secret to be re-pushed
	// on the next apply. Require it whenever a secret is configured, otherwise
	// changing the secret in config would be a silent no-op.
	secretConfigured := isSet(data.Token) || isSet(data.AccessToken) || isSet(data.SecretValue)
	if secretConfigured && isMissing(data.TokenWoVersion) {
		resp.Diagnostics.AddAttributeError(
			path.Root("token_wo_version"),
			"Missing required attribute",
			"\"token_wo_version\" must be set when a write-only secret (token / access_token / secret_value) is configured. "+
				"It is the rotation trigger: Terraform cannot diff write-only values, so increment it to re-push the secret. Set it to 1 on initial creation.",
		)
	}
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
		if errors.As(err, &apierr) && apierr.StatusCode == 404 {
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

	// Re-push auth on any change to a mutable auth field. token_wo_version covers
	// write-only secret rotation (token / access_token / refresh_token / client_secret /
	// secret_value, which Terraform cannot diff). The remaining mutable, non-secret
	// auth fields are networking (environment_variable) and expires_at / refresh
	// (mcp_oauth); immutable auth fields are RequiresReplace and never reach Update.
	needsAuthUpdate := !plan.TokenWoVersion.Equal(state.TokenWoVersion)
	switch plan.Type.ValueString() {
	case "environment_variable":
		if !plan.Networking.Equal(state.Networking) {
			needsAuthUpdate = true
		}
	case "mcp_oauth":
		if !plan.ExpiresAt.Equal(state.ExpiresAt) || !plan.Refresh.Equal(state.Refresh) {
			needsAuthUpdate = true
		}
	}

	if needsAuthUpdate {
		authUpdate, d := buildUpdateAuthUnion(ctx, req.Config, &plan)
		resp.Diagnostics.Append(d...)
		if resp.Diagnostics.HasError() {
			return
		}
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
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to archive vault credential: %s", err))
		}
		return
	}

	_, err := r.client.Beta.Vaults.Credentials.Delete(ctx, data.ID.ValueString(), anthropic.BetaVaultCredentialDeleteParams{
		VaultID: data.VaultID.ValueString(),
	})
	if err != nil {
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

// buildCreateAuthUnion builds the BetaVaultCredentialNewParamsAuthUnion from plan + config (write-only).
// Write-only values are read from cfg (tfsdk.Config) because they are absent from plan/state.
func buildCreateAuthUnion(ctx context.Context, cfg tfsdk.Config, data *VaultCredentialResourceModel) (anthropic.BetaVaultCredentialNewParamsAuthUnion, diag.Diagnostics) {
	var diags diag.Diagnostics

	switch data.Type.ValueString() {
	case "static_bearer":
		var token types.String
		diags.Append(cfg.GetAttribute(ctx, path.Root("token"), &token)...)
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

	case "mcp_oauth":
		var accessToken types.String
		diags.Append(cfg.GetAttribute(ctx, path.Root("access_token"), &accessToken)...)
		if diags.HasError() {
			return anthropic.BetaVaultCredentialNewParamsAuthUnion{}, diags
		}

		oauthParams := &anthropic.BetaManagedAgentsMCPOAuthCreateParams{
			AccessToken:  accessToken.ValueString(),
			MCPServerURL: data.MCPServerURL.ValueString(),
			Type:         "mcp_oauth",
		}

		if !data.ExpiresAt.IsNull() && !data.ExpiresAt.IsUnknown() {
			t, err := time.Parse(time.RFC3339, data.ExpiresAt.ValueString())
			if err != nil {
				diags.AddAttributeError(path.Root("expires_at"), "Invalid timestamp", fmt.Sprintf("Cannot parse expires_at as RFC3339: %s", err))
				return anthropic.BetaVaultCredentialNewParamsAuthUnion{}, diags
			}
			oauthParams.ExpiresAt = param.NewOpt(t)
		}

		if !data.Refresh.IsNull() && !data.Refresh.IsUnknown() {
			refreshParams, d := buildRefreshParams(ctx, cfg, data.Refresh)
			diags.Append(d...)
			if diags.HasError() {
				return anthropic.BetaVaultCredentialNewParamsAuthUnion{}, diags
			}
			oauthParams.Refresh = refreshParams
		}

		return anthropic.BetaVaultCredentialNewParamsAuthUnion{OfMCPOAuth: oauthParams}, diags

	case "environment_variable":
		var secretValue types.String
		diags.Append(cfg.GetAttribute(ctx, path.Root("secret_value"), &secretValue)...)
		if diags.HasError() {
			return anthropic.BetaVaultCredentialNewParamsAuthUnion{}, diags
		}

		networking, d := buildNetworkingUnion(ctx, data.Networking)
		diags.Append(d...)
		if diags.HasError() {
			return anthropic.BetaVaultCredentialNewParamsAuthUnion{}, diags
		}

		return anthropic.BetaVaultCredentialNewParamsAuthUnion{
			OfEnvironmentVariable: &anthropic.BetaManagedAgentsEnvironmentVariableCreateParams{
				SecretName:  data.SecretName.ValueString(),
				SecretValue: secretValue.ValueString(),
				Networking:  networking,
				Type:        "environment_variable",
			},
		}, diags
	}

	diags.AddError("Internal Error", fmt.Sprintf("Unknown credential type: %s", data.Type.ValueString()))
	return anthropic.BetaVaultCredentialNewParamsAuthUnion{}, diags
}

// buildUpdateAuthUnion builds the BetaVaultCredentialUpdateParamsAuthUnion for updates.
func buildUpdateAuthUnion(ctx context.Context, cfg tfsdk.Config, data *VaultCredentialResourceModel) (anthropic.BetaVaultCredentialUpdateParamsAuthUnion, diag.Diagnostics) {
	var diags diag.Diagnostics

	switch data.Type.ValueString() {
	case "static_bearer":
		var token types.String
		diags.Append(cfg.GetAttribute(ctx, path.Root("token"), &token)...)
		if diags.HasError() {
			return anthropic.BetaVaultCredentialUpdateParamsAuthUnion{}, diags
		}
		updateParams := &anthropic.BetaManagedAgentsStaticBearerUpdateParams{
			Type: "static_bearer",
		}
		if !token.IsNull() && !token.IsUnknown() && token.ValueString() != "" {
			updateParams.Token = param.NewOpt(token.ValueString())
		}
		return anthropic.BetaVaultCredentialUpdateParamsAuthUnion{OfStaticBearer: updateParams}, diags

	case "mcp_oauth":
		var accessToken types.String
		diags.Append(cfg.GetAttribute(ctx, path.Root("access_token"), &accessToken)...)
		if diags.HasError() {
			return anthropic.BetaVaultCredentialUpdateParamsAuthUnion{}, diags
		}

		oauthUpdate := &anthropic.BetaManagedAgentsMCPOAuthUpdateParams{
			Type: "mcp_oauth",
		}
		if !accessToken.IsNull() && !accessToken.IsUnknown() && accessToken.ValueString() != "" {
			oauthUpdate.AccessToken = param.NewOpt(accessToken.ValueString())
		}

		if !data.ExpiresAt.IsNull() && !data.ExpiresAt.IsUnknown() {
			t, err := time.Parse(time.RFC3339, data.ExpiresAt.ValueString())
			if err != nil {
				diags.AddAttributeError(path.Root("expires_at"), "Invalid timestamp", fmt.Sprintf("Cannot parse expires_at as RFC3339: %s", err))
				return anthropic.BetaVaultCredentialUpdateParamsAuthUnion{}, diags
			}
			oauthUpdate.ExpiresAt = param.NewOpt(t)
		}

		if !data.Refresh.IsNull() && !data.Refresh.IsUnknown() {
			refreshUpdate, d := buildRefreshUpdateParams(ctx, cfg, data.Refresh)
			diags.Append(d...)
			if diags.HasError() {
				return anthropic.BetaVaultCredentialUpdateParamsAuthUnion{}, diags
			}
			oauthUpdate.Refresh = refreshUpdate
		}

		return anthropic.BetaVaultCredentialUpdateParamsAuthUnion{OfMCPOAuth: oauthUpdate}, diags

	case "environment_variable":
		var secretValue types.String
		diags.Append(cfg.GetAttribute(ctx, path.Root("secret_value"), &secretValue)...)
		if diags.HasError() {
			return anthropic.BetaVaultCredentialUpdateParamsAuthUnion{}, diags
		}

		envUpdate := &anthropic.BetaManagedAgentsEnvironmentVariableUpdateParams{
			Type: "environment_variable",
		}
		if !secretValue.IsNull() && !secretValue.IsUnknown() && secretValue.ValueString() != "" {
			envUpdate.SecretValue = param.NewOpt(secretValue.ValueString())
		}

		if !data.Networking.IsNull() && !data.Networking.IsUnknown() {
			networking, d := buildNetworkingUnion(ctx, data.Networking)
			diags.Append(d...)
			if diags.HasError() {
				return anthropic.BetaVaultCredentialUpdateParamsAuthUnion{}, diags
			}
			envUpdate.Networking = networking
		}

		return anthropic.BetaVaultCredentialUpdateParamsAuthUnion{OfEnvironmentVariable: envUpdate}, diags
	}

	diags.AddError("Internal Error", fmt.Sprintf("Unknown credential type: %s", data.Type.ValueString()))
	return anthropic.BetaVaultCredentialUpdateParamsAuthUnion{}, diags
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

	var refreshToken types.String
	diags.Append(cfg.GetAttribute(ctx, path.Root("refresh").AtName("refresh_token"), &refreshToken)...)
	if diags.HasError() {
		return anthropic.BetaManagedAgentsMCPOAuthRefreshUpdateParams{}, diags
	}

	updateParams := anthropic.BetaManagedAgentsMCPOAuthRefreshUpdateParams{}

	if !refreshToken.IsNull() && !refreshToken.IsUnknown() && refreshToken.ValueString() != "" {
		updateParams.RefreshToken = param.NewOpt(refreshToken.ValueString())
	}
	if !refreshModel.Scope.IsNull() && !refreshModel.Scope.IsUnknown() {
		updateParams.Scope = param.NewOpt(refreshModel.Scope.ValueString())
	}

	if !refreshModel.TokenEndpointAuth.IsNull() && !refreshModel.TokenEndpointAuth.IsUnknown() {
		var teaModel credentialTokenEndpointAuthModel
		diags.Append(refreshModel.TokenEndpointAuth.As(ctx, &teaModel, basetypes.ObjectAsOptions{})...)
		if diags.HasError() {
			return anthropic.BetaManagedAgentsMCPOAuthRefreshUpdateParams{}, diags
		}

		teaType := teaModel.Type.ValueString()
		if teaType == "client_secret_basic" || teaType == "client_secret_post" {
			var cs types.String
			diags.Append(cfg.GetAttribute(ctx, path.Root("refresh").AtName("token_endpoint_auth").AtName("client_secret"), &cs)...)
			if diags.HasError() {
				return anthropic.BetaManagedAgentsMCPOAuthRefreshUpdateParams{}, diags
			}
			if teaType == "client_secret_basic" {
				tea := &anthropic.BetaManagedAgentsTokenEndpointAuthBasicUpdateParam{
					Type: "client_secret_basic",
				}
				if !cs.IsNull() && !cs.IsUnknown() && cs.ValueString() != "" {
					tea.ClientSecret = param.NewOpt(cs.ValueString())
				}
				updateParams.TokenEndpointAuth = anthropic.BetaManagedAgentsMCPOAuthRefreshUpdateParamsTokenEndpointAuthUnion{
					OfClientSecretBasic: tea,
				}
			} else {
				tea := &anthropic.BetaManagedAgentsTokenEndpointAuthPostUpdateParam{
					Type: "client_secret_post",
				}
				if !cs.IsNull() && !cs.IsUnknown() && cs.ValueString() != "" {
					tea.ClientSecret = param.NewOpt(cs.ValueString())
				}
				updateParams.TokenEndpointAuth = anthropic.BetaManagedAgentsMCPOAuthRefreshUpdateParamsTokenEndpointAuthUnion{
					OfClientSecretPost: tea,
				}
			}
		}
	}

	return updateParams, diags
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
	return anthropic.BetaManagedAgentsCredentialNetworkingParamsOfLimited(hosts), diags
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
	} else if !data.Metadata.IsNull() {
		data.Metadata = types.MapNull(types.StringType)
	}

	// Map auth response back into non-secret state fields.
	// The union struct exposes all variant fields as flat fields (populated by JSON unmarshaling).
	// Secret fields (token, access_token, refresh_token, client_secret, secret_value) are NEVER set.
	authType := cred.Auth.Type
	data.Type = types.StringValue(authType)

	switch authType {
	case "static_bearer":
		// MCPServerURL is a flat field on the union struct.
		data.MCPServerURL = types.StringValue(cred.Auth.MCPServerURL)
		// Clear oauth/env-var fields
		data.ExpiresAt = types.StringNull()
		data.Refresh = types.ObjectNull(credentialRefreshAttrTypes)
		data.SecretName = types.StringNull()
		data.Networking = types.ObjectNull(credentialNetworkingAttrTypes)

	case "mcp_oauth":
		data.MCPServerURL = types.StringValue(cred.Auth.MCPServerURL)
		if cred.Auth.ExpiresAt.IsZero() {
			data.ExpiresAt = types.StringNull()
		} else {
			data.ExpiresAt = types.StringValue(cred.Auth.ExpiresAt.Format(time.RFC3339))
		}
		// Map refresh (non-secret fields only) — Refresh is a flat field on the union struct.
		if cred.Auth.JSON.Refresh.Valid() {
			refreshObj, d := mapRefreshResponseToObject(ctx, cred.Auth.Refresh)
			diags.Append(d...)
			data.Refresh = refreshObj
		} else {
			data.Refresh = types.ObjectNull(credentialRefreshAttrTypes)
		}
		// Clear env-var fields
		data.SecretName = types.StringNull()
		data.Networking = types.ObjectNull(credentialNetworkingAttrTypes)

	case "environment_variable":
		// SecretName and Networking are flat fields on the union struct.
		data.SecretName = types.StringValue(cred.Auth.SecretName)
		// Map networking
		networkingObj, d := mapNetworkingResponseToObject(ctx, cred.Auth.Networking)
		diags.Append(d...)
		data.Networking = networkingObj
		// Clear oauth/bearer fields
		data.MCPServerURL = types.StringNull()
		data.ExpiresAt = types.StringNull()
		data.Refresh = types.ObjectNull(credentialRefreshAttrTypes)
	}

	return diags
}

// mapRefreshResponseToObject maps a BetaManagedAgentsMCPOAuthRefreshResponse to a types.Object.
// refresh_token is write-only and client_secret is never in the response, so both are null.
func mapRefreshResponseToObject(ctx context.Context, refresh anthropic.BetaManagedAgentsMCPOAuthRefreshResponse) (types.Object, diag.Diagnostics) {
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
