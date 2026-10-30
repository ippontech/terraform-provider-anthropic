// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package environments

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
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
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &EnvironmentResource{}
var _ resource.ResourceWithImportState = &EnvironmentResource{}
var _ resource.ResourceWithModifyPlan = &EnvironmentResource{}

func NewEnvironmentResource() resource.Resource {
	return &EnvironmentResource{}
}

// EnvironmentResource defines the resource implementation.
type EnvironmentResource struct {
	client *anthropic.Client
}

// --- Terraform data models ---

type EnvironmentResourceModel struct {
	ID               types.String `tfsdk:"id"`
	Name             types.String `tfsdk:"name"`
	Description      types.String `tfsdk:"description"`
	Metadata         types.Map    `tfsdk:"metadata"`
	Config           types.Object `tfsdk:"config"`
	ArchivedAt       types.String `tfsdk:"archived_at"`
	CreatedAt        types.String `tfsdk:"created_at"`
	UpdatedAt        types.String `tfsdk:"updated_at"`
	Type             types.String `tfsdk:"type"`
	ArchiveOnDestroy types.Bool   `tfsdk:"archive_on_destroy"`
}

type environmentConfigModel struct {
	Type       types.String `tfsdk:"type"`
	Networking types.Object `tfsdk:"networking"`
	Packages   types.Object `tfsdk:"packages"`
}

type environmentNetworkingModel struct {
	Type                 types.String `tfsdk:"type"`
	AllowMCPServers      types.Bool   `tfsdk:"allow_mcp_servers"`
	AllowPackageManagers types.Bool   `tfsdk:"allow_package_managers"`
	AllowedHosts         types.List   `tfsdk:"allowed_hosts"`
}

type environmentPackagesModel struct {
	Apt   types.List `tfsdk:"apt"`
	Cargo types.List `tfsdk:"cargo"`
	Gem   types.List `tfsdk:"gem"`
	Go    types.List `tfsdk:"go"`
	Npm   types.List `tfsdk:"npm"`
	Pip   types.List `tfsdk:"pip"`
}

// --- Attribute type maps for nested objects ---

var environmentNetworkingAttrTypes = map[string]attr.Type{
	"type":                   types.StringType,
	"allow_mcp_servers":      types.BoolType,
	"allow_package_managers": types.BoolType,
	"allowed_hosts":          types.ListType{ElemType: types.StringType},
}

var environmentPackagesAttrTypes = map[string]attr.Type{
	"apt":   types.ListType{ElemType: types.StringType},
	"cargo": types.ListType{ElemType: types.StringType},
	"gem":   types.ListType{ElemType: types.StringType},
	"go":    types.ListType{ElemType: types.StringType},
	"npm":   types.ListType{ElemType: types.StringType},
	"pip":   types.ListType{ElemType: types.StringType},
}

var environmentConfigAttrTypes = map[string]attr.Type{
	"type":       types.StringType,
	"networking": types.ObjectType{AttrTypes: environmentNetworkingAttrTypes},
	"packages":   types.ObjectType{AttrTypes: environmentPackagesAttrTypes},
}

// --- Schema ---

func (r *EnvironmentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_environment"
}

func (r *EnvironmentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Creates and manages a cloud environment (container template) for Anthropic Managed Agent sessions (beta). " +
			"Environments define the networking policy and pre-installed packages available to agent sessions.",
		Attributes: map[string]schema.Attribute{
			// --- Required ---
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Human-readable name for the environment.",
			},

			// --- Optional ---
			// Not Computed: removing description or metadata from the
			// configuration must plan their removal, which UseStateForUnknown
			// on an Optional+Computed attribute silently prevented.
			"description": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Optional description of the environment. Removing it from the configuration clears it.",
			},
			"metadata": schema.MapAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "User-provided metadata key-value pairs. Keys removed from the configuration are deleted; removing the attribute deletes every key.",
			},

			// --- Optional+Computed ---
			"config": schema.SingleNestedAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Cloud environment configuration including networking and packages.",
				PlanModifiers:       []planmodifier.Object{objectplanmodifier.UseStateForUnknown()},
				Attributes: map[string]schema.Attribute{
					"type": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "Environment configuration type. Always `cloud`.",
					},
					"networking": schema.SingleNestedAttribute{
						Optional:            true,
						Computed:            true,
						MarkdownDescription: "Network configuration policy for the environment.",
						PlanModifiers:       []planmodifier.Object{objectplanmodifier.UseStateForUnknown()},
						Attributes: map[string]schema.Attribute{
							"type": schema.StringAttribute{
								Required:            true,
								MarkdownDescription: "Network policy type. One of `unrestricted` or `limited`.",
								Validators:          []validator.String{stringvalidator.OneOf("unrestricted", "limited")},
							},
							"allow_mcp_servers": schema.BoolAttribute{
								Optional:            true,
								Computed:            true,
								MarkdownDescription: "Permits outbound access to MCP server endpoints. Only applicable for `limited` networking.",
							},
							"allow_package_managers": schema.BoolAttribute{
								Optional:            true,
								Computed:            true,
								MarkdownDescription: "Permits outbound access to public package registries. Only applicable for `limited` networking.",
							},
							"allowed_hosts": schema.ListAttribute{
								Optional:            true,
								Computed:            true,
								ElementType:         types.StringType,
								MarkdownDescription: "Specifies domains the container can reach. Only applicable for `limited` networking.",
							},
						},
					},
					"packages": schema.SingleNestedAttribute{
						Optional:            true,
						Computed:            true,
						MarkdownDescription: "Package manager configuration for pre-installed packages.",
						PlanModifiers:       []planmodifier.Object{objectplanmodifier.UseStateForUnknown()},
						Attributes: map[string]schema.Attribute{
							"apt": schema.ListAttribute{
								Optional:            true,
								Computed:            true,
								ElementType:         types.StringType,
								MarkdownDescription: "Ubuntu/Debian packages to install.",
							},
							"cargo": schema.ListAttribute{
								Optional:            true,
								Computed:            true,
								ElementType:         types.StringType,
								MarkdownDescription: "Rust packages to install.",
							},
							"gem": schema.ListAttribute{
								Optional:            true,
								Computed:            true,
								ElementType:         types.StringType,
								MarkdownDescription: "Ruby packages to install.",
							},
							"go": schema.ListAttribute{
								Optional:            true,
								Computed:            true,
								ElementType:         types.StringType,
								MarkdownDescription: "Go packages to install.",
							},
							"npm": schema.ListAttribute{
								Optional:            true,
								Computed:            true,
								ElementType:         types.StringType,
								MarkdownDescription: "Node.js packages to install.",
							},
							"pip": schema.ListAttribute{
								Optional:            true,
								Computed:            true,
								ElementType:         types.StringType,
								MarkdownDescription: "Python packages to install.",
							},
						},
					},
				},
			},

			// --- Computed ---
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Unique environment identifier assigned by the API (e.g. `env_...`).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"type": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Object type. Always `environment`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
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
				MarkdownDescription: "Archive timestamp (RFC 3339). Null if the environment has not been archived.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"archive_on_destroy": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "If `true`, destroying this resource archives the environment instead of permanently deleting it. Default: `false`.",
				PlanModifiers:       []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

// --- Configure ---

func (r *EnvironmentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ModifyPlan keeps updated_at stable on no-op plans and marks it unknown when
// mutable attributes are changing, to avoid inconsistent-result-after-apply errors.
func (r *EnvironmentResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	// Only relevant during updates (both state and plan are non-null).
	if req.Plan.Raw.IsNull() || req.State.Raw.IsNull() {
		return
	}

	var state, plan EnvironmentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if environmentChanged(plan, state) {
		// Resource is being updated; updated_at will change.
		plan.UpdatedAt = types.StringUnknown()
	} else {
		// No-op plan; keep updated_at stable to avoid a spurious diff.
		plan.UpdatedAt = state.UpdatedAt
	}
	resp.Diagnostics.Append(resp.Plan.Set(ctx, plan)...)
}

// environmentChanged reports whether plan changes an attribute the API
// holds. archive_on_destroy, the only other configurable attribute, is
// local-only.
func environmentChanged(plan, state EnvironmentResourceModel) bool {
	return !plan.Name.Equal(state.Name) ||
		!plan.Description.Equal(state.Description) ||
		!plan.Metadata.Equal(state.Metadata) ||
		!plan.Config.Equal(state.Config)
}

// --- Create ---

func (r *EnvironmentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data EnvironmentResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	params := anthropic.BetaEnvironmentNewParams{
		Name: data.Name.ValueString(),
	}

	if !data.Description.IsNull() && !data.Description.IsUnknown() {
		params.Description = param.NewOpt(data.Description.ValueString())
	}

	if !data.Metadata.IsNull() && !data.Metadata.IsUnknown() {
		var meta map[string]string
		resp.Diagnostics.Append(data.Metadata.ElementsAs(ctx, &meta, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
		params.Metadata = meta
	}

	if !data.Config.IsNull() && !data.Config.IsUnknown() {
		configParams, diags := buildEnvironmentConfigParams(ctx, data.Config)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		params.Config = anthropic.BetaEnvironmentNewParamsConfigUnion{OfCloud: &configParams}
	}

	env, err := r.client.Beta.Environments.New(ctx, params)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create environment: %s", err))
		return
	}

	resp.Diagnostics.Append(mapEnvironmentResponseToState(ctx, env, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Read ---

func (r *EnvironmentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data EnvironmentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	env, err := r.client.Beta.Environments.Get(ctx, data.ID.ValueString(), anthropic.BetaEnvironmentGetParams{})
	if err != nil {
		// Deleted out of band: drop it from state so the next plan re-creates it.
		if providerrors.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read environment: %s", err))
		return
	}

	resp.Diagnostics.Append(mapEnvironmentResponseToState(ctx, env, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// archive_on_destroy is local-only (not in the API); default to false when not already set (e.g. on import).
	if data.ArchiveOnDestroy.IsNull() || data.ArchiveOnDestroy.IsUnknown() {
		data.ArchiveOnDestroy = types.BoolValue(false)
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Update ---

func (r *EnvironmentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data EnvironmentResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state EnvironmentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Only archive_on_destroy changes: nothing to send, and an API write
	// would move updated_at, which ModifyPlan planned unchanged.
	if !environmentChanged(data, state) {
		state.ArchiveOnDestroy = data.ArchiveOnDestroy
		resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
		return
	}

	params := anthropic.BetaEnvironmentUpdateParams{
		Name: param.NewOpt(data.Name.ValueString()),
	}

	// The update endpoint keeps an omitted description and clears a null one
	// (https://platform.claude.com/docs/en/api/beta/environments/update), so
	// a description removed from the configuration is sent as null.
	if !data.Description.IsNull() && !data.Description.IsUnknown() {
		params.Description = param.NewOpt(data.Description.ValueString())
	} else if data.Description.IsNull() && !state.Description.IsNull() {
		params.Description = param.Null[string]()
	}

	// metadata is a patch: keys removed from the configuration are sent as null.
	metaPatch, diags := tfvalue.MetadataPatch(ctx, data.Metadata, state.Metadata)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if len(metaPatch) > 0 {
		params.SetExtraFields(map[string]any{"metadata": metaPatch})
	}

	if !data.Config.IsNull() && !data.Config.IsUnknown() {
		configParams, diags := buildEnvironmentConfigParams(ctx, data.Config)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		params.Config = anthropic.BetaEnvironmentUpdateParamsConfigUnion{OfCloud: &configParams}
	}

	env, err := r.client.Beta.Environments.Update(ctx, state.ID.ValueString(), params)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update environment: %s", err))
		return
	}

	resp.Diagnostics.Append(mapEnvironmentResponseToState(ctx, env, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Delete ---

func (r *EnvironmentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data EnvironmentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// A 404 means the environment is already gone, which is what destroy wants.
	if data.ArchiveOnDestroy.ValueBool() {
		_, err := r.client.Beta.Environments.Archive(ctx, data.ID.ValueString(), anthropic.BetaEnvironmentArchiveParams{})
		if err != nil && !providerrors.IsNotFound(err) {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to archive environment: %s", err))
		}
		return
	}

	_, err := r.client.Beta.Environments.Delete(ctx, data.ID.ValueString(), anthropic.BetaEnvironmentDeleteParams{})
	if err != nil && !providerrors.IsNotFound(err) {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete environment: %s", err))
	}
}

// --- ImportState ---

func (r *EnvironmentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// ============================================================================
// Helper functions
// ============================================================================

// buildEnvironmentConfigParams converts a Terraform config object to SDK params.
func buildEnvironmentConfigParams(ctx context.Context, configObj types.Object) (anthropic.BetaCloudConfigParams, diag.Diagnostics) {
	var diags diag.Diagnostics
	var configModel environmentConfigModel

	diags.Append(configObj.As(ctx, &configModel, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return anthropic.BetaCloudConfigParams{}, diags
	}

	configParams := anthropic.BetaCloudConfigParams{}

	if isKnown(configModel.Networking) {
		networking, d := buildNetworkingParams(ctx, configModel.Networking)
		diags.Append(d...)
		if diags.HasError() {
			return anthropic.BetaCloudConfigParams{}, diags
		}
		configParams.Networking = networking
	}

	if isKnown(configModel.Packages) {
		packages, d := buildPackagesParams(ctx, configModel.Packages)
		diags.Append(d...)
		if diags.HasError() {
			return anthropic.BetaCloudConfigParams{}, diags
		}
		configParams.Packages = packages
	}

	return configParams, diags
}

// isKnown reports whether v is neither null nor unknown: only known values
// are sent, so the API keeps (update) or defaults (create) the others.
func isKnown(v attr.Value) bool {
	return !v.IsNull() && !v.IsUnknown()
}

// buildNetworkingParams converts a known networking object to SDK params:
// "unrestricted" sends its type alone, any other type a limited network with
// its known fields (an empty allowed_hosts is left out).
func buildNetworkingParams(ctx context.Context, obj types.Object) (anthropic.BetaCloudConfigParamsNetworkingUnion, diag.Diagnostics) {
	var model environmentNetworkingModel
	diags := obj.As(ctx, &model, basetypes.ObjectAsOptions{})
	if diags.HasError() {
		return anthropic.BetaCloudConfigParamsNetworkingUnion{}, diags
	}

	if model.Type.ValueString() == "unrestricted" {
		return anthropic.BetaCloudConfigParamsNetworkingUnion{
			OfUnrestricted: &anthropic.BetaUnrestrictedNetworkParam{Type: "unrestricted"},
		}, diags
	}

	limited := &anthropic.BetaLimitedNetworkParams{}
	if isKnown(model.AllowMCPServers) {
		limited.AllowMCPServers = param.NewOpt(model.AllowMCPServers.ValueBool())
	}
	if isKnown(model.AllowPackageManagers) {
		limited.AllowPackageManagers = param.NewOpt(model.AllowPackageManagers.ValueBool())
	}
	// An empty list is sent as []: the update endpoint keeps an omitted
	// allowed_hosts, so leaving it out cannot clear the hosts.
	diags.Append(knownStrings(ctx, model.AllowedHosts, &limited.AllowedHosts)...)
	if diags.HasError() {
		return anthropic.BetaCloudConfigParamsNetworkingUnion{}, diags
	}
	return anthropic.BetaCloudConfigParamsNetworkingUnion{OfLimited: limited}, diags
}

// buildPackagesParams converts a known packages object to SDK params: each
// known list is sent, an empty one as [].
func buildPackagesParams(ctx context.Context, obj types.Object) (anthropic.BetaPackagesParams, diag.Diagnostics) {
	var model environmentPackagesModel
	diags := obj.As(ctx, &model, basetypes.ObjectAsOptions{})
	if diags.HasError() {
		return anthropic.BetaPackagesParams{}, diags
	}

	var params anthropic.BetaPackagesParams
	for _, pkg := range []struct {
		list   types.List
		target *[]string
	}{
		{model.Apt, &params.Apt},
		{model.Cargo, &params.Cargo},
		{model.Gem, &params.Gem},
		{model.Go, &params.Go},
		{model.Npm, &params.Npm},
		{model.Pip, &params.Pip},
	} {
		diags.Append(knownStrings(ctx, pkg.list, pkg.target)...)
		if diags.HasError() {
			return anthropic.BetaPackagesParams{}, diags
		}
	}
	return params, diags
}

// knownStrings reads list into target when it is known and leaves target
// alone otherwise.
func knownStrings(ctx context.Context, list types.List, target *[]string) diag.Diagnostics {
	if !isKnown(list) {
		return nil
	}
	return list.ElementsAs(ctx, target, false)
}

// mapEnvironmentResponseToState maps the API response to the Terraform state model.
func mapEnvironmentResponseToState(ctx context.Context, env *anthropic.BetaEnvironment, data *EnvironmentResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics

	data.ID = types.StringValue(env.ID)
	data.Name = types.StringValue(env.Name)
	data.Type = types.StringValue(string(env.Type))
	data.CreatedAt = types.StringValue(env.CreatedAt)
	data.UpdatedAt = types.StringValue(env.UpdatedAt)

	if env.ArchivedAt == "" {
		data.ArchivedAt = types.StringNull()
	} else {
		data.ArchivedAt = types.StringValue(env.ArchivedAt)
	}

	if env.Description != "" {
		data.Description = types.StringValue(env.Description)
	} else if !data.Description.IsNull() {
		data.Description = types.StringNull()
	}

	// Metadata
	if len(env.Metadata) > 0 {
		elements := make(map[string]attr.Value, len(env.Metadata))
		for k, v := range env.Metadata {
			elements[k] = types.StringValue(v)
		}
		metaMap, d := types.MapValue(types.StringType, elements)
		diags.Append(d...)
		data.Metadata = metaMap
	} else {
		data.Metadata = tfvalue.MetadataAfterEmptyResponse(data.Metadata)
	}

	// Config — always set since API always returns it
	allowedHosts, d := types.ListValueFrom(ctx, types.StringType, env.Config.Networking.AllowedHosts)
	diags.Append(d...)

	networkingObj, d := types.ObjectValue(environmentNetworkingAttrTypes, map[string]attr.Value{
		"type":                   types.StringValue(env.Config.Networking.Type),
		"allow_mcp_servers":      types.BoolValue(env.Config.Networking.AllowMCPServers),
		"allow_package_managers": types.BoolValue(env.Config.Networking.AllowPackageManagers),
		"allowed_hosts":          allowedHosts,
	})
	diags.Append(d...)

	aptList, d := types.ListValueFrom(ctx, types.StringType, env.Config.Packages.Apt)
	diags.Append(d...)
	cargoList, d := types.ListValueFrom(ctx, types.StringType, env.Config.Packages.Cargo)
	diags.Append(d...)
	gemList, d := types.ListValueFrom(ctx, types.StringType, env.Config.Packages.Gem)
	diags.Append(d...)
	goList, d := types.ListValueFrom(ctx, types.StringType, env.Config.Packages.Go)
	diags.Append(d...)
	npmList, d := types.ListValueFrom(ctx, types.StringType, env.Config.Packages.Npm)
	diags.Append(d...)
	pipList, d := types.ListValueFrom(ctx, types.StringType, env.Config.Packages.Pip)
	diags.Append(d...)

	packagesObj, d := types.ObjectValue(environmentPackagesAttrTypes, map[string]attr.Value{
		"apt":   aptList,
		"cargo": cargoList,
		"gem":   gemList,
		"go":    goList,
		"npm":   npmList,
		"pip":   pipList,
	})
	diags.Append(d...)

	configObj, d := types.ObjectValue(environmentConfigAttrTypes, map[string]attr.Value{
		"type":       types.StringValue(string(env.Config.Type)),
		"networking": networkingObj,
		"packages":   packagesObj,
	})
	diags.Append(d...)
	data.Config = configObj

	return diags
}
