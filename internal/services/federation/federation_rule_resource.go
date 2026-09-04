// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64default"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &FederationRuleResource{}
var _ resource.ResourceWithImportState = &FederationRuleResource{}
var _ resource.ResourceWithConfigValidators = &FederationRuleResource{}

func NewFederationRuleResource() resource.Resource {
	return &FederationRuleResource{}
}

// FederationRuleResource defines the resource implementation. It uses the
// OAuth bearer client: the federation endpoints reject API keys outright.
type FederationRuleResource struct {
	client *providerdata.OAuthClient
}

// --- Terraform data models ---

// FederationRuleResourceModel describes the resource data model.
type FederationRuleResourceModel struct {
	ID                     types.String `tfsdk:"id"`
	Name                   types.String `tfsdk:"name"`
	Description            types.String `tfsdk:"description"`
	IssuerID               types.String `tfsdk:"issuer_id"`
	Match                  types.Object `tfsdk:"match"`
	Target                 types.Object `tfsdk:"target"`
	OAuthScope             types.String `tfsdk:"oauth_scope"`
	WorkspaceID            types.String `tfsdk:"workspace_id"`
	AppliesToAllWorkspaces types.Bool   `tfsdk:"applies_to_all_workspaces"`
	TokenLifetimeSeconds   types.Int64  `tfsdk:"token_lifetime_seconds"`
	Attributes             types.Map    `tfsdk:"attributes"`
	IssuerName             types.String `tfsdk:"issuer_name"`
	WorkspaceIDs           types.List   `tfsdk:"workspace_ids"`
	CreatedAt              types.String `tfsdk:"created_at"`
	UpdatedAt              types.String `tfsdk:"updated_at"`
	ArchivedAt             types.String `tfsdk:"archived_at"`
	CreatedByActorID       types.String `tfsdk:"created_by_actor_id"`
	UpdatedByActorID       types.String `tfsdk:"updated_by_actor_id"`
	ArchivedByActorID      types.String `tfsdk:"archived_by_actor_id"`
}

// federationRuleMatchModel holds the nested match block.
type federationRuleMatchModel struct {
	SubjectPrefix types.String `tfsdk:"subject_prefix"`
	Audience      types.String `tfsdk:"audience"`
	Claims        types.Map    `tfsdk:"claims"`
	Condition     types.String `tfsdk:"condition"`
}

// federationRuleTargetModel holds the nested target block.
type federationRuleTargetModel struct {
	ServiceAccountID   types.String `tfsdk:"service_account_id"`
	ServiceAccountName types.String `tfsdk:"service_account_name"`
}

// --- Attribute type maps for nested objects ---

var federationRuleMatchAttrTypes = map[string]attr.Type{
	"subject_prefix": types.StringType,
	"audience":       types.StringType,
	"claims":         types.MapType{ElemType: types.StringType},
	"condition":      types.StringType,
}

var federationRuleTargetAttrTypes = map[string]attr.Type{
	"service_account_id":   types.StringType,
	"service_account_name": types.StringType,
}

// federationRuleNameRegex matches the slug format the API requires for `name`:
// lowercase letters, digits, and hyphens only.
var federationRuleNameRegex = regexp.MustCompile(`^[a-z0-9-]+$`)

// --- Schema ---

func (r *FederationRuleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_federation_rule"
}

func (r *FederationRuleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a Workload Identity Federation rule, which binds a federation issuer to a service account: " +
			"JWTs matching the rule's `match` conditions mint OAuth access tokens acting as the `target` service account.",
		Attributes: map[string]schema.Attribute{
			// --- Identity ---
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Unique federation rule identifier (e.g. `fdrl_...`).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},

			// --- Required ---
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Slug identifier (lowercase letters, digits, hyphens), 1-255 characters. Unique within the organization; a duplicate name returns a 409.",
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 255),
					stringvalidator.RegexMatches(federationRuleNameRegex, "must contain only lowercase letters, digits, and hyphens"),
				},
			},
			"issuer_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Tagged ID of the federation issuer whose tokens this rule accepts. Immutable: the update API has no `issuer_id` parameter, so changing it forces replacement.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"match": schema.SingleNestedAttribute{
				Required:            true,
				MarkdownDescription: "Conditions the verified JWT must satisfy for this rule to apply. All populated fields must pass. At least one of `subject_prefix`, `claims`, or `condition` is required; `audience` alone is not sufficient (rejected by the API).",
				Attributes: map[string]schema.Attribute{
					"subject_prefix": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "Match the verified JWT `sub` claim. Exact match unless the value ends with `*`, in which case it is a prefix match. Example: `repo:my-org/my-repo:ref:refs/heads/main`.",
					},
					"audience": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "Exact match against the `aud` claim (any element if array). When omitted, the JWT's `aud` must still equal Anthropic's expected audience for the issuer; setting this field overrides that default.",
					},
					"claims": schema.MapAttribute{
						Optional:            true,
						ElementType:         types.StringType,
						MarkdownDescription: "Exact-match `{claim: value}` pairs against top-level claims. Only string-valued claims can be matched; use `condition` for non-string claims.",
					},
					"condition": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "CEL expression over claims for logic the structural fields can't express. Must evaluate to a boolean and may reference only the `claims` variable; a constant-true expression (such as `true`) is rejected with 400.",
					},
				},
			},
			"target": schema.SingleNestedAttribute{
				Required:            true,
				MarkdownDescription: "Identity that tokens minted via this rule act as. Currently always a `service_account` target.",
				Attributes: map[string]schema.Attribute{
					"service_account_id": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "Tagged ID of the service account to mint tokens for. Whether this can be changed in place is undocumented by the API; kept updatable and left for the API to arbitrate.",
					},
					"service_account_name": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "Service account's display name at read time. Ignored on writes; never set this, it is populated from the API response.",
						PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
					},
				},
			},
			"oauth_scope": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Space-separated OAuth scopes granted on minted tokens. One of `workspace:developer`, `workspace:inference`, `workspace:manage_tunnels`, `org:admin`. " +
					"OAuth callers (this provider) may only create or modify rules whose scope is `workspace:developer` or `workspace:inference`; the other two require a Console session but remain importable and readable.",
				Validators: []validator.String{
					stringvalidator.OneOf("workspace:developer", "workspace:inference", "workspace:manage_tunnels", "org:admin"),
				},
			},

			// --- Optional ---
			"description": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Optional free-text description.",
			},
			"workspace_id": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Tagged ID of the workspace to enable this rule for. Exactly one of `workspace_id` or `applies_to_all_workspaces = true` must be set.",
			},
			"applies_to_all_workspaces": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "When true, enable this rule for every workspace in the org (including workspaces created later). Exactly one of `workspace_id` or `applies_to_all_workspaces = true` must be set.",
			},
			"token_lifetime_seconds": schema.Int64Attribute{
				Optional:            true,
				Computed:            true,
				Default:             int64default.StaticInt64(3600),
				MarkdownDescription: "Lifetime in seconds for access tokens minted via this rule. Minted tokens are capped at `max(60, min(this value, 2 x remaining assertion validity))` seconds. Default: `3600` (1h).",
				Validators:          []validator.Int64{int64validator.Between(60, 86400)},
				PlanModifiers:       []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
			},
			"attributes": schema.MapAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "CEL expressions `{name: expr}` extracting named values from claims. Not yet supported by the API; any non-empty value is rejected with 400.",
			},

			// --- Computed ---
			"issuer_name": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Issuer's display name at read time.",
			},
			"workspace_ids": schema.ListAttribute{
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Tagged IDs of the workspaces this rule is enabled for. May be empty for older rules that only carry the legacy `workspace_id` binding.",
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
				MarkdownDescription: "Archive timestamp (RFC 3339). Null if the rule has not been archived.",
			},
			"created_by_actor_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Tagged ID (`user_`/`svac_`) of the actor that created this rule.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"updated_by_actor_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Tagged ID (`user_`/`svac_`) of the actor that last updated this rule.",
			},
			"archived_by_actor_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Tagged ID (`user_`/`svac_`) of the actor that archived this rule. Null if the rule has not been archived.",
			},
		},
	}
}

// --- ConfigValidators ---

func (r *FederationRuleResource) ConfigValidators(_ context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		&federationRuleConfigValidator{},
	}
}

// federationRuleConfigValidator validates the two cross-attribute constraints
// the API enforces: `match` must carry at least one of subject_prefix/claims/
// condition, and exactly one of workspace_id / applies_to_all_workspaces=true
// must be set. Both checks are Unknown-safe: an unresolved reference (e.g. a
// value coming from another resource/data source) is treated as neither set
// nor missing, so it never trips a check that may still be satisfied once the
// value resolves at apply time. See CLAUDE.md's ConfigValidator Unknown
// handling section and vaultCredentialConfigValidator for the same pattern.
type federationRuleConfigValidator struct{}

func (v *federationRuleConfigValidator) Description(_ context.Context) string {
	return "Validates federation rule match conditions and workspace targeting."
}

func (v *federationRuleConfigValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v *federationRuleConfigValidator) ValidateResource(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var data FederationRuleResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(validateFederationRuleConfig(ctx, data)...)
}

// validateFederationRuleConfig holds the pure validation logic so it can be
// unit tested directly against a FederationRuleResourceModel, without needing
// to marshal one through a tfsdk.Config (which requires assembling a full
// tftypes.Value by hand).
func validateFederationRuleConfig(ctx context.Context, data FederationRuleResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics

	isSet := func(v attr.Value) bool { return !v.IsNull() && !v.IsUnknown() }

	// --- match: at least one of subject_prefix / claims / condition ---
	if isSet(data.Match) {
		var match federationRuleMatchModel
		diags.Append(data.Match.As(ctx, &match, basetypes.ObjectAsOptions{})...)
		if diags.HasError() {
			return diags
		}

		subjectPrefixUnknown := match.SubjectPrefix.IsUnknown()
		claimsUnknown := match.Claims.IsUnknown()
		conditionUnknown := match.Condition.IsUnknown()

		// If any of the three could still resolve to a value, do not flag the
		// config yet: it may become valid once the reference is known.
		if !subjectPrefixUnknown && !claimsUnknown && !conditionUnknown {
			subjectPrefixMissing := match.SubjectPrefix.IsNull()
			claimsMissing := match.Claims.IsNull()
			conditionMissing := match.Condition.IsNull()

			if subjectPrefixMissing && claimsMissing && conditionMissing {
				diags.AddAttributeError(
					path.Root("match"),
					"Missing required attribute",
					"At least one of \"match.subject_prefix\", \"match.claims\", or \"match.condition\" must be set. "+
						"\"match.audience\" alone is not sufficient; the API rejects that combination.",
				)
			}
		}
	}

	// --- exactly one of workspace_id / applies_to_all_workspaces=true ---
	workspaceIDUnknown := data.WorkspaceID.IsUnknown()
	appliesUnknown := data.AppliesToAllWorkspaces.IsUnknown()

	if !workspaceIDUnknown && !appliesUnknown {
		workspaceIDSet := isSet(data.WorkspaceID)
		appliesTrue := isSet(data.AppliesToAllWorkspaces) && data.AppliesToAllWorkspaces.ValueBool()

		switch {
		case workspaceIDSet && appliesTrue:
			diags.AddError(
				"Conflicting attributes",
				"Exactly one of \"workspace_id\" or \"applies_to_all_workspaces = true\" must be set, not both.",
			)
		case !workspaceIDSet && !appliesTrue:
			diags.AddError(
				"Missing required attribute",
				"Exactly one of \"workspace_id\" or \"applies_to_all_workspaces = true\" must be set.",
			)
		}
	}

	return diags
}

// --- Configure ---

func (r *FederationRuleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *FederationRuleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data FederationRuleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	matchParam, diags := buildMatchParam(ctx, data.Match)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	targetParam, diags := buildTargetParam(ctx, data.Target)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	params := anthropic.BetaOrganizationFederationRuleNewParams{
		IssuerID:   data.IssuerID.ValueString(),
		Match:      matchParam,
		Name:       data.Name.ValueString(),
		OAuthScope: data.OAuthScope.ValueString(),
		Target:     targetParam,
	}

	if !data.Description.IsNull() && !data.Description.IsUnknown() {
		params.Description = param.NewOpt(data.Description.ValueString())
	}
	if !data.WorkspaceID.IsNull() && !data.WorkspaceID.IsUnknown() {
		params.WorkspaceID = param.NewOpt(data.WorkspaceID.ValueString())
	}
	if !data.AppliesToAllWorkspaces.IsNull() && !data.AppliesToAllWorkspaces.IsUnknown() {
		params.AppliesToAllWorkspaces = param.NewOpt(data.AppliesToAllWorkspaces.ValueBool())
	}
	if !data.TokenLifetimeSeconds.IsNull() && !data.TokenLifetimeSeconds.IsUnknown() {
		params.TokenLifetimeSeconds = param.NewOpt(data.TokenLifetimeSeconds.ValueInt64())
	}
	if !data.Attributes.IsNull() && !data.Attributes.IsUnknown() {
		var attrsMap map[string]string
		resp.Diagnostics.Append(data.Attributes.ElementsAs(ctx, &attrsMap, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
		params.Attributes = attrsMap
	}

	rule, err := r.client.Beta.Organization.Federation.Rules.New(ctx, params)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create federation rule: %s", err))
		return
	}

	resp.Diagnostics.Append(mapFederationRuleToState(ctx, rule, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Read ---

func (r *FederationRuleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data FederationRuleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	rule, err := r.client.Beta.Organization.Federation.Rules.Get(ctx, data.ID.ValueString(), anthropic.BetaOrganizationFederationRuleGetParams{})
	if err != nil {
		// The rule was deleted out-of-band: drop it from state so the next
		// plan recreates it instead of erroring forever.
		var apierr *anthropic.Error
		if errors.As(err, &apierr) && apierr.StatusCode == 404 {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read federation rule: %s", err))
		return
	}

	resp.Diagnostics.Append(mapFederationRuleToState(ctx, rule, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Update ---

func (r *FederationRuleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan FederationRuleResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state FederationRuleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	matchParam, diags := buildMatchParam(ctx, plan.Match)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	targetParam, diags := buildTargetParam(ctx, plan.Target)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// match and target are replaced as whole objects by the update API, and
	// both are Required attributes in this schema, so they are always present
	// in the plan and always re-sent.
	params := anthropic.BetaOrganizationFederationRuleUpdateParams{
		Match:      matchParam,
		Name:       param.NewOpt(plan.Name.ValueString()),
		OAuthScope: param.NewOpt(plan.OAuthScope.ValueString()),
		Target:     targetParam,
	}

	// description is nullable server-side: send the desired value, or an
	// explicit null to clear it (omitting it would leave the old value).
	if plan.Description.IsUnknown() {
		// Not expected for a non-computed optional attribute; leave omitted.
	} else if plan.Description.IsNull() {
		params.Description = param.Null[string]()
	} else {
		params.Description = param.NewOpt(plan.Description.ValueString())
	}

	if !plan.TokenLifetimeSeconds.IsNull() && !plan.TokenLifetimeSeconds.IsUnknown() {
		params.TokenLifetimeSeconds = param.NewOpt(plan.TokenLifetimeSeconds.ValueInt64())
	}

	if !plan.WorkspaceID.IsNull() && !plan.WorkspaceID.IsUnknown() {
		params.WorkspaceID = param.NewOpt(plan.WorkspaceID.ValueString())
	}

	// applies_to_all_workspaces is always resolved explicitly to true or false
	// when it changes. The ConfigValidator guarantees the config carries
	// exactly one of workspace_id / applies_to_all_workspaces=true at any
	// given time, so switching from applies_to_all_workspaces=true to a
	// workspace_id binding removes this attribute from config (plan value
	// null). Omitting the field in that case would leave the server's true
	// value in place, and the rule would keep applying to every workspace
	// instead of just the newly configured one — so an explicit `false` is
	// sent whenever the prior state was true and the plan no longer sets it.
	switch {
	case !plan.AppliesToAllWorkspaces.IsNull() && !plan.AppliesToAllWorkspaces.IsUnknown():
		params.AppliesToAllWorkspaces = param.NewOpt(plan.AppliesToAllWorkspaces.ValueBool())
	case !state.AppliesToAllWorkspaces.IsNull() && state.AppliesToAllWorkspaces.ValueBool():
		params.AppliesToAllWorkspaces = param.NewOpt(false)
	}

	// attributes: replace-whole-value semantics (send null to clear). Only
	// sent when changed, since the API rejects any non-empty value today.
	if !plan.Attributes.Equal(state.Attributes) {
		if plan.Attributes.IsNull() || plan.Attributes.IsUnknown() {
			params.SetExtraFields(map[string]any{"attributes": nil})
		} else {
			var attrsMap map[string]string
			resp.Diagnostics.Append(plan.Attributes.ElementsAs(ctx, &attrsMap, false)...)
			if resp.Diagnostics.HasError() {
				return
			}
			params.Attributes = attrsMap
		}
	}

	rule, err := r.client.Beta.Organization.Federation.Rules.Update(ctx, state.ID.ValueString(), params)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update federation rule: %s", err))
		return
	}

	resp.Diagnostics.Append(mapFederationRuleToState(ctx, rule, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// --- Delete ---

// Delete always archives: the API has no hard-delete endpoint for federation
// rules, so there is no archive_on_destroy attribute to gate this.
func (r *FederationRuleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data FederationRuleResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.Beta.Organization.Federation.Rules.Archive(ctx, data.ID.ValueString(), anthropic.BetaOrganizationFederationRuleArchiveParams{})
	if err != nil {
		var apierr *anthropic.Error
		if errors.As(err, &apierr) && apierr.StatusCode == 404 {
			// Already gone — treat as success.
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to archive federation rule: %s", err))
	}
}

// --- ImportState ---

func (r *FederationRuleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// ============================================================================
// Helper functions
// ============================================================================

// buildMatchParam builds a BetaFederationRuleMatchParam from the `match` object.
func buildMatchParam(ctx context.Context, matchObj types.Object) (anthropic.BetaFederationRuleMatchParam, diag.Diagnostics) {
	var diags diag.Diagnostics
	var m federationRuleMatchModel
	diags.Append(matchObj.As(ctx, &m, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return anthropic.BetaFederationRuleMatchParam{}, diags
	}

	params := anthropic.BetaFederationRuleMatchParam{}

	if !m.SubjectPrefix.IsNull() && !m.SubjectPrefix.IsUnknown() {
		params.SubjectPrefix = param.NewOpt(m.SubjectPrefix.ValueString())
	}
	if !m.Audience.IsNull() && !m.Audience.IsUnknown() {
		params.Audience = param.NewOpt(m.Audience.ValueString())
	}
	if !m.Condition.IsNull() && !m.Condition.IsUnknown() {
		params.Condition = param.NewOpt(m.Condition.ValueString())
	}
	if !m.Claims.IsNull() && !m.Claims.IsUnknown() {
		var claims map[string]string
		diags.Append(m.Claims.ElementsAs(ctx, &claims, false)...)
		if diags.HasError() {
			return anthropic.BetaFederationRuleMatchParam{}, diags
		}
		params.Claims = claims
	}

	return params, diags
}

// buildTargetParam builds a BetaServiceAccountTargetParam from the `target`
// object. service_account_name is ignored on writes, so it is never sent.
func buildTargetParam(ctx context.Context, targetObj types.Object) (anthropic.BetaServiceAccountTargetParam, diag.Diagnostics) {
	var diags diag.Diagnostics
	var t federationRuleTargetModel
	diags.Append(targetObj.As(ctx, &t, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return anthropic.BetaServiceAccountTargetParam{}, diags
	}

	return anthropic.BetaServiceAccountTargetParam{
		ServiceAccountID: t.ServiceAccountID.ValueString(),
	}, diags
}

// mapMatchResponseToObject maps a BetaFederationRuleMatch response to a types.Object.
func mapMatchResponseToObject(match anthropic.BetaFederationRuleMatch) (types.Object, diag.Diagnostics) {
	var diags diag.Diagnostics

	subjectPrefix := types.StringNull()
	if match.SubjectPrefix != "" {
		subjectPrefix = types.StringValue(match.SubjectPrefix)
	}
	audience := types.StringNull()
	if match.Audience != "" {
		audience = types.StringValue(match.Audience)
	}
	condition := types.StringNull()
	if match.Condition != "" {
		condition = types.StringValue(match.Condition)
	}

	claims := types.MapNull(types.StringType)
	if len(match.Claims) > 0 {
		elements := make(map[string]attr.Value, len(match.Claims))
		for k, v := range match.Claims {
			elements[k] = types.StringValue(v)
		}
		claimsMap, d := types.MapValue(types.StringType, elements)
		diags.Append(d...)
		claims = claimsMap
	}

	obj, d := types.ObjectValue(federationRuleMatchAttrTypes, map[string]attr.Value{
		"subject_prefix": subjectPrefix,
		"audience":       audience,
		"claims":         claims,
		"condition":      condition,
	})
	diags.Append(d...)
	if diags.HasError() {
		return types.ObjectNull(federationRuleMatchAttrTypes), diags
	}

	return obj, diags
}

// mapTargetResponseToObject maps a BetaServiceAccountTarget response to a types.Object.
func mapTargetResponseToObject(target anthropic.BetaServiceAccountTarget) (types.Object, diag.Diagnostics) {
	serviceAccountName := types.StringNull()
	if target.ServiceAccountName != "" {
		serviceAccountName = types.StringValue(target.ServiceAccountName)
	}

	return types.ObjectValue(federationRuleTargetAttrTypes, map[string]attr.Value{
		"service_account_id":   types.StringValue(target.ServiceAccountID),
		"service_account_name": serviceAccountName,
	})
}

// mapFederationRuleToState maps the API response to the Terraform state model.
func mapFederationRuleToState(ctx context.Context, rule *anthropic.BetaFederationRule, data *FederationRuleResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics

	data.ID = types.StringValue(rule.ID)
	data.Name = types.StringValue(rule.Name)
	data.IssuerID = types.StringValue(rule.IssuerID)
	data.IssuerName = types.StringValue(rule.IssuerName)
	data.OAuthScope = types.StringValue(rule.OAuthScope)
	data.AppliesToAllWorkspaces = types.BoolValue(rule.AppliesToAllWorkspaces)
	data.TokenLifetimeSeconds = types.Int64Value(rule.TokenLifetimeSeconds)

	if rule.Description != "" {
		data.Description = types.StringValue(rule.Description)
	} else {
		data.Description = types.StringNull()
	}

	// Legacy single-workspace binding. Mapped directly from the API response
	// (empty string -> null); the exact clearing behaviour when switching
	// to/from applies_to_all_workspaces=true is undocumented, so this mirrors
	// the same "let the API arbitrate" approach used for target above.
	if rule.WorkspaceID != "" {
		data.WorkspaceID = types.StringValue(rule.WorkspaceID)
	} else {
		data.WorkspaceID = types.StringNull()
	}

	workspaceIDs, d := types.ListValueFrom(ctx, types.StringType, rule.WorkspaceIDs)
	diags.Append(d...)
	data.WorkspaceIDs = workspaceIDs

	// Attributes: not yet supported by the API (always null today), but
	// mapped generically in case that changes.
	if len(rule.Attributes) > 0 {
		elements := make(map[string]attr.Value, len(rule.Attributes))
		for k, v := range rule.Attributes {
			elements[k] = types.StringValue(v)
		}
		attrsMap, d := types.MapValue(types.StringType, elements)
		diags.Append(d...)
		data.Attributes = attrsMap
	} else {
		data.Attributes = types.MapNull(types.StringType)
	}

	// Timestamps + actor IDs: "" / zero-time -> null.
	if rule.CreatedAt.IsZero() {
		data.CreatedAt = types.StringNull()
	} else {
		data.CreatedAt = types.StringValue(rule.CreatedAt.Format(time.RFC3339))
	}
	if rule.UpdatedAt.IsZero() {
		data.UpdatedAt = types.StringNull()
	} else {
		data.UpdatedAt = types.StringValue(rule.UpdatedAt.Format(time.RFC3339))
	}
	if rule.ArchivedAt.IsZero() {
		data.ArchivedAt = types.StringNull()
	} else {
		data.ArchivedAt = types.StringValue(rule.ArchivedAt.Format(time.RFC3339))
	}
	if rule.CreatedByActorID != "" {
		data.CreatedByActorID = types.StringValue(rule.CreatedByActorID)
	} else {
		data.CreatedByActorID = types.StringNull()
	}
	if rule.UpdatedByActorID != "" {
		data.UpdatedByActorID = types.StringValue(rule.UpdatedByActorID)
	} else {
		data.UpdatedByActorID = types.StringNull()
	}
	if rule.ArchivedByActorID != "" {
		data.ArchivedByActorID = types.StringValue(rule.ArchivedByActorID)
	} else {
		data.ArchivedByActorID = types.StringNull()
	}

	matchObj, d := mapMatchResponseToObject(rule.Match)
	diags.Append(d...)
	data.Match = matchObj

	targetObj, d := mapTargetResponseToObject(rule.Target)
	diags.Append(d...)
	data.Target = targetObj

	return diags
}
