// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package userprofiles

import (
	"context"
	"errors"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &UserProfileResource{}
var _ resource.ResourceWithImportState = &UserProfileResource{}

func NewUserProfileResource() resource.Resource {
	return &UserProfileResource{}
}

// UserProfileResource defines the resource implementation.
type UserProfileResource struct {
	client *anthropic.Client
}

// UserProfileResourceModel describes the resource data model.
type UserProfileResourceModel struct {
	ID          types.String `tfsdk:"id"`
	AccessType  types.String `tfsdk:"access_type"`
	ExternalID  types.String `tfsdk:"external_id"`
	Name        types.String `tfsdk:"name"`
	Metadata    types.Map    `tfsdk:"metadata"`
	TrustGrants types.Map    `tfsdk:"trust_grants"`
	Type        types.String `tfsdk:"type"`
	CreatedAt   types.String `tfsdk:"created_at"`
	UpdatedAt   types.String `tfsdk:"updated_at"`
}

// --- Schema ---

func (r *UserProfileResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_user_profile"
}

func (r *UserProfileResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Creates and manages a user profile (beta): an identity representing an end-user or resold-to " +
			"company that a platform is acting on behalf of when calling the API. Not generally available for all " +
			"organizations; the API returns `404` for organizations without this beta enabled. There is no delete " +
			"endpoint: destroying this resource only removes it from Terraform state, it does not delete the profile " +
			"at Anthropic.",
		Attributes: map[string]schema.Attribute{
			// --- Required ---
			"access_type": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "How the platform uses the API on behalf of the entity this profile represents. " +
					"`application`: the platform sells a product that uses the API behind the scenes, and the profile " +
					"represents an individual end-user of that product. `passthrough`: the platform resells raw " +
					"inference, and the profile identifies the resold-to company.",
				Validators: []validator.String{stringvalidator.OneOf("application", "passthrough")},
			},

			// --- Optional ---
			"external_id": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Platform's own identifier for this user. Not enforced unique. 1-255 characters " +
					"(the API's own maximum is 255; a minimum of 1 is enforced here because an empty string round-trips " +
					"through the API as null, which would otherwise make `external_id = \"\"` an inconsistent-apply error).",
				Validators: []validator.String{stringvalidator.LengthBetween(1, 255)},
			},
			"name": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Real-world name of the entity this profile represents (company or individual). " +
					"For a resold-to company this is that company's name. 1-255 characters (see `external_id` for why " +
					"the empty string is rejected).",
				Validators: []validator.String{stringvalidator.LengthBetween(1, 255)},
			},
			"metadata": schema.MapAttribute{
				Optional:    true,
				ElementType: types.StringType,
				MarkdownDescription: "Arbitrary key-value metadata. Up to 16 pairs; keys 1-64 characters; values 1-512 " +
					"characters (empty values are rejected by the API — updates use an empty string internally to " +
					"remove a key, but a value configured here must be non-empty).",
				Validators: []validator.Map{
					mapvalidator.SizeAtMost(16),
					mapvalidator.KeysAre(stringvalidator.LengthBetween(1, 64)),
					mapvalidator.ValueStringsAre(stringvalidator.LengthBetween(1, 512)),
				},
			},

			// --- Computed ---
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Unique identifier for this user profile assigned by the API (prefixed `uprof_`).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"trust_grants": schema.MapNestedAttribute{
				Computed: true,
				MarkdownDescription: "Trust grants for this profile, keyed by grant name. A key is omitted when no " +
					"grant is active or in flight for it.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"status": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Status of the trust grant: `active`, `pending`, or `rejected`.",
						},
					},
				},
			},
			"type": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Object type. Always `user_profile`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Creation timestamp (RFC 3339).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Timestamp of the last update (RFC 3339).",
			},
		},
	}
}

// --- Configure ---

func (r *UserProfileResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *UserProfileResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data UserProfileResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	params, diags := buildUserProfileCreateParams(ctx, data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	plannedMetadata := data.Metadata

	profile, err := r.client.Beta.UserProfiles.New(ctx, params)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create user profile: %s", err))
		return
	}

	resp.Diagnostics.Append(mapUserProfileToState(profile, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.Metadata = preserveEmptyMetadata(plannedMetadata, data.Metadata)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Read ---

func (r *UserProfileResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data UserProfileResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	priorMetadata := data.Metadata

	profile, err := r.client.Beta.UserProfiles.Get(ctx, data.ID.ValueString(), anthropic.BetaUserProfileGetParams{})
	if err != nil {
		var apierr *anthropic.Error
		if errors.As(err, &apierr) && apierr.StatusCode == 404 {
			// A 404 here is ambiguous: it means the profile really was deleted
			// out-of-band, but it is also what this same endpoint returns for an
			// organization where the user profiles beta is not (or is no longer)
			// enabled, or for a key that lost access to it (see the resource's
			// MarkdownDescription). In the latter cases every profile in state
			// looks "gone" on the next refresh, which silently drops them from
			// state; a subsequent apply would then try to recreate them, and
			// since there is no delete endpoint, any duplicates created once the
			// beta comes back can never be cleaned up automatically. There is no
			// way to distinguish the two cases from the response alone, so warn
			// instead of failing outright.
			resp.Diagnostics.AddWarning(
				"User Profile Not Found",
				fmt.Sprintf("User profile %q was not found (404) and has been removed from Terraform state. "+
					"This also occurs if the user profiles beta is not enabled for the organization, or if the "+
					"configured API key no longer has access to it — in either case the profile may still exist. "+
					"Verify independently before assuming it was deleted.", data.ID.ValueString()),
			)
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read user profile: %s", err))
		return
	}

	resp.Diagnostics.Append(mapUserProfileToState(profile, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.Metadata = preserveEmptyMetadata(priorMetadata, data.Metadata)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Update ---

func (r *UserProfileResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data UserProfileResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state UserProfileResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	params, diags := buildUserProfileUpdateParams(ctx, data, state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	plannedMetadata := data.Metadata

	profile, err := r.client.Beta.UserProfiles.Update(ctx, state.ID.ValueString(), params)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update user profile: %s", err))
		return
	}

	resp.Diagnostics.Append(mapUserProfileToState(profile, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.Metadata = preserveEmptyMetadata(plannedMetadata, data.Metadata)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Delete ---

func (r *UserProfileResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data UserProfileResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// There is no delete endpoint for user profiles. Removing this resource from
	// Terraform state is all that can be done; the profile continues to exist at
	// Anthropic until removed some other way.
	msg := fmt.Sprintf("User profile %q has no delete endpoint. It has been removed from Terraform state only; "+
		"it still exists at Anthropic.", data.ID.ValueString())
	resp.Diagnostics.AddWarning("User Profile Not Deleted", msg)
	tflog.Warn(ctx, msg)
}

// --- ImportState ---

func (r *UserProfileResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// ============================================================================
// Helper functions
// ============================================================================

// buildUserProfileCreateParams builds the New params from the planned config.
func buildUserProfileCreateParams(ctx context.Context, data UserProfileResourceModel) (anthropic.BetaUserProfileNewParams, diag.Diagnostics) {
	var diags diag.Diagnostics

	params := anthropic.BetaUserProfileNewParams{
		AccessType: anthropic.BetaUserProfileNewParamsAccessType(data.AccessType.ValueString()),
	}
	if !data.ExternalID.IsNull() {
		params.ExternalID = param.NewOpt(data.ExternalID.ValueString())
	}
	if !data.Name.IsNull() {
		params.Name = param.NewOpt(data.Name.ValueString())
	}
	if !data.Metadata.IsNull() && !data.Metadata.IsUnknown() {
		var meta map[string]string
		diags.Append(data.Metadata.ElementsAs(ctx, &meta, false)...)
		if diags.HasError() {
			return params, diags
		}
		params.Metadata = meta
	}

	return params, diags
}

// buildUserProfileUpdateParams builds the Update params by diffing the
// planned config against prior state. access_type is always resent (the API
// requires it on every update); external_id and name are only sent when
// changed, and a value removed from config is cleared by sending an explicit
// JSON null (param.Null[string]()) rather than an empty string, since the API
// documents both fields as nullable and the repo's PATCH convention (see
// buildMetadataPatch in vaults) is to null a field to clear it rather than
// send an empty string, which some APIs reject as invalid input for a field
// that is otherwise validated as non-empty. metadata keeps its own
// merge-semantics helper (buildUserProfileMetadataUpdate) since it behaves
// differently: a key is cleared with an empty string, not null.
func buildUserProfileUpdateParams(ctx context.Context, data, state UserProfileResourceModel) (anthropic.BetaUserProfileUpdateParams, diag.Diagnostics) {
	params := anthropic.BetaUserProfileUpdateParams{
		AccessType: anthropic.BetaUserProfileUpdateParamsAccessType(data.AccessType.ValueString()),
	}

	if !data.ExternalID.Equal(state.ExternalID) {
		if data.ExternalID.IsNull() {
			params.ExternalID = param.Null[string]()
		} else {
			params.ExternalID = param.NewOpt(data.ExternalID.ValueString())
		}
	}
	if !data.Name.Equal(state.Name) {
		if data.Name.IsNull() {
			params.Name = param.Null[string]()
		} else {
			params.Name = param.NewOpt(data.Name.ValueString())
		}
	}

	metaUpdate, diags := buildUserProfileMetadataUpdate(ctx, data.Metadata, state.Metadata)
	if diags.HasError() {
		return params, diags
	}
	if len(metaUpdate) > 0 {
		params.Metadata = metaUpdate
	}

	return params, diags
}

// mapUserProfileToState maps the API response into the resource's state model.
func mapUserProfileToState(profile *anthropic.BetaUserProfile, data *UserProfileResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics

	data.ID = types.StringValue(profile.ID)
	data.AccessType = types.StringValue(string(profile.AccessType))
	data.Type = types.StringValue(string(profile.Type))
	data.CreatedAt = tfvalue.TimeOrNull(profile.CreatedAt)
	data.UpdatedAt = tfvalue.TimeOrNull(profile.UpdatedAt)
	data.ExternalID = tfvalue.StringOrNull(profile.ExternalID)
	data.Name = tfvalue.StringOrNull(profile.Name)

	metaMap, d := userProfileMetadataToMap(profile.Metadata)
	diags.Append(d...)
	data.Metadata = metaMap

	if grantsMap, ok, d := userProfileTrustGrantsToMap(profile.TrustGrants); ok {
		diags.Append(d...)
		data.TrustGrants = grantsMap
	} else {
		diags.Append(d...)
		grantsMap, d := types.MapValue(userProfileTrustGrantObjectType, map[string]attr.Value{})
		diags.Append(d...)
		data.TrustGrants = grantsMap
	}

	return diags
}

// preserveEmptyMetadata keeps the caller's previously known metadata value
// (the plan on Create/Update, prior state on Read) when the API response maps
// to a null map. metadata is Optional but not Computed, so Terraform requires
// the final state to exactly equal the planned/config value; but the API
// response can't distinguish an empty map ({}) from metadata being absent —
// both round-trip as a zero-length Go map, which maps to MapNull. Without
// this, `metadata = {}` in config would flip to null in state and fail with
// "Provider produced inconsistent result after apply". Same pattern as
// internal/services/memorystores.
func preserveEmptyMetadata(known, fromAPI types.Map) types.Map {
	if fromAPI.IsNull() && !known.IsNull() && !known.IsUnknown() && len(known.Elements()) == 0 {
		return known
	}
	return fromAPI
}

// buildUserProfileMetadataUpdate computes the merge-semantics update body for
// metadata. The API merges keys provided (overwriting existing values) and
// leaves omitted keys unchanged; a key is removed by sending it with an empty
// string value. To converge declarative config with server state, this
// upserts every planned key and sends an empty string for keys removed since
// the prior state. Returns an empty map when there is nothing to change.
func buildUserProfileMetadataUpdate(ctx context.Context, plan, state types.Map) (map[string]string, diag.Diagnostics) {
	var diags diag.Diagnostics
	update := map[string]string{}

	var pm map[string]string
	if !plan.IsNull() && !plan.IsUnknown() {
		diags.Append(plan.ElementsAs(ctx, &pm, false)...)
		if diags.HasError() {
			return nil, diags
		}
		for k, v := range pm {
			update[k] = v
		}
	}

	if !state.IsNull() && !state.IsUnknown() {
		var sm map[string]string
		diags.Append(state.ElementsAs(ctx, &sm, false)...)
		if diags.HasError() {
			return nil, diags
		}
		for k := range sm {
			if _, stillPresent := pm[k]; !stillPresent {
				update[k] = ""
			}
		}
	}

	return update, diags
}
