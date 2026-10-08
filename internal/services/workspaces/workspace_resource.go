// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &WorkspaceResource{}
var _ resource.ResourceWithImportState = &WorkspaceResource{}

func NewWorkspaceResource() resource.Resource {
	return &WorkspaceResource{}
}

// WorkspaceResource defines the resource implementation.
type WorkspaceResource struct {
	adminClient *admin.Client
}

// --- Terraform data models ---

type WorkspaceResourceModel struct {
	ID            types.String `tfsdk:"id"`
	Name          types.String `tfsdk:"name"`
	DataResidency types.Object `tfsdk:"data_residency"`
	ArchivedAt    types.String `tfsdk:"archived_at"`
	CreatedAt     types.String `tfsdk:"created_at"`
	DisplayColor  types.String `tfsdk:"display_color"`
	Type          types.String `tfsdk:"type"`

	Tags                   types.Map    `tfsdk:"tags"`
	ExternalKeyID          types.String `tfsdk:"external_key_id"`
	CompartmentID          types.String `tfsdk:"compartment_id"`
	UserProfileID          types.String `tfsdk:"user_profile_id"`
	InferenceDataRetention types.Object `tfsdk:"inference_data_retention"`
}

// workspaceInferenceDataRetentionAttrTypes describes inference_data_retention.
// Neither the pinned SDK nor the public reference declares this field; the only
// shape observed on the live API (2026-10-08) is {"type":"disabled"}.
var workspaceInferenceDataRetentionAttrTypes = map[string]attr.Type{
	"type": types.StringType,
}

type workspaceDataResidencyModel struct {
	AllowedInferenceGeos types.List   `tfsdk:"allowed_inference_geos"`
	DefaultInferenceGeo  types.String `tfsdk:"default_inference_geo"`
	WorkspaceGeo         types.String `tfsdk:"workspace_geo"`
}

var workspaceDataResidencyAttrTypes = map[string]attr.Type{
	"allowed_inference_geos": types.ListType{ElemType: types.StringType},
	"default_inference_geo":  types.StringType,
	"workspace_geo":          types.StringType,
}

// --- Admin API request/response types ---

type workspaceAPIResponse struct {
	ID            string                    `json:"id"`
	ArchivedAt    *string                   `json:"archived_at"`
	CreatedAt     string                    `json:"created_at"`
	DataResidency workspaceAPIDataResidency `json:"data_residency"`
	DisplayColor  string                    `json:"display_color"`
	Name          string                    `json:"name"`
	Type          string                    `json:"type"`

	Tags                   map[string]string                   `json:"tags"`
	ExternalKeyID          *string                             `json:"external_key_id"`
	CompartmentID          *string                             `json:"compartment_id"`
	UserProfileID          *string                             `json:"user_profile_id"`
	InferenceDataRetention *workspaceAPIInferenceDataRetention `json:"inference_data_retention"`
}

type workspaceAPIInferenceDataRetention struct {
	Type string `json:"type"`
}

type workspaceAPIDataResidency struct {
	// AllowedInferenceGeos is either the string "unrestricted" or an array of strings.
	AllowedInferenceGeos json.RawMessage `json:"allowed_inference_geos"`
	DefaultInferenceGeo  string          `json:"default_inference_geo"`
	WorkspaceGeo         string          `json:"workspace_geo"`
}

type workspaceCreateRequest struct {
	Name          string                        `json:"name"`
	DataResidency *workspaceCreateDataResidency `json:"data_residency,omitempty"`
	Tags          map[string]string             `json:"tags,omitempty"`
	ExternalKeyID string                        `json:"external_key_id,omitempty"`
}

type workspaceCreateDataResidency struct {
	// AllowedInferenceGeos is serialised as either "unrestricted" or a string array.
	AllowedInferenceGeos json.RawMessage `json:"allowed_inference_geos,omitempty"`
	DefaultInferenceGeo  string          `json:"default_inference_geo,omitempty"`
	WorkspaceGeo         string          `json:"workspace_geo,omitempty"`
}

type workspaceUpdateRequest struct {
	Name          string                        `json:"name"`
	DataResidency *workspaceUpdateDataResidency `json:"data_residency,omitempty"`
	// Tags carries the tag patch: planned keys upsert, keys removed since prior
	// state are sent as null (see buildWorkspaceTagsPatch).
	Tags map[string]any `json:"tags,omitempty"`
}

type workspaceUpdateDataResidency struct {
	AllowedInferenceGeos json.RawMessage `json:"allowed_inference_geos,omitempty"`
	DefaultInferenceGeo  string          `json:"default_inference_geo,omitempty"`
}

// --- Schema ---

func (r *WorkspaceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_workspace"
}

func (r *WorkspaceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Creates and manages an Anthropic Workspace via the Admin API. " +
			"Requires `admin_api_key` (or `ANTHROPIC_ADMIN_API_KEY`) to be configured on the provider. " +
			"Deleting this resource **archives** the workspace rather than permanently deleting it, " +
			"because the Anthropic API does not expose a delete operation for workspaces.",
		Attributes: map[string]schema.Attribute{
			// --- Writable ---
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Human-readable name for the workspace.",
			},
			"data_residency": schema.SingleNestedAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Data-residency configuration. Defaults applied by the API when omitted.",
				PlanModifiers:       []planmodifier.Object{objectplanmodifier.UseStateForUnknown()},
				Attributes: map[string]schema.Attribute{
					"allowed_inference_geos": schema.ListAttribute{
						Optional:            true,
						Computed:            true,
						ElementType:         types.StringType,
						MarkdownDescription: "Permitted inference geo values. Use `[\"unrestricted\"]` to allow all geos, or list specific geos such as `[\"us\", \"eu\"]`.",
					},
					"default_inference_geo": schema.StringAttribute{
						Optional:            true,
						Computed:            true,
						MarkdownDescription: "Default inference geo applied when requests omit the parameter.",
					},
					"workspace_geo": schema.StringAttribute{
						Optional:            true,
						Computed:            true,
						MarkdownDescription: "Geographic region for workspace data storage. Immutable after creation — changing this forces a new resource.",
						PlanModifiers: []planmodifier.String{
							stringplanmodifier.RequiresReplace(),
							stringplanmodifier.UseStateForUnknown(),
						},
					},
				},
			},

			"tags": schema.MapAttribute{
				Optional:    true,
				Computed:    true,
				ElementType: types.StringType,
				MarkdownDescription: "User-defined tags as string key-value pairs. Keys must not begin with `anthropic` (case-insensitive). " +
					"Omit to leave the tags unmanaged; to remove all tags, set `tags = {}`.",
				PlanModifiers: []planmodifier.Map{mapplanmodifier.UseStateForUnknown()},
				Validators:    []validator.Map{mapvalidator.KeysAre(noAnthropicPrefixValidator{})},
			},
			"external_key_id": schema.StringAttribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "ID (`ekey_...`) of the customer-managed encryption key (CMEK) configuration used to encrypt this workspace's data. " +
					"Requires CMEK to be enabled for the organization. **Write-once**: the API cannot detach or replace a key once attached, " +
					"so changing this value forces a new workspace. When omitted, a key attached outside Terraform is kept in state without forcing a replacement.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
					stringplanmodifier.RequiresReplace(),
				},
			},

			// --- Computed ---
			"compartment_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Identifier of the workspace's encryption compartment. Reference it in the KMS key policy when configuring CMEK on AWS.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"user_profile_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "ID of the user profile associated with the workspace, or null if none.",
			},
			"inference_data_retention": schema.SingleNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Inference data retention setting reported by the API, or null if absent. Read-only.",
				Attributes: map[string]schema.Attribute{
					"type": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "Retention mode, for example `disabled`.",
					},
				},
			},
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Unique workspace identifier assigned by the API.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"archived_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the workspace was archived, or null if it is active.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the workspace was created.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"display_color": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Hex color code representing the workspace in the Anthropic Console.",
			},
			"type": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Object type. Always `workspace`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

// --- Configure ---

func (r *WorkspaceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

	if !providerrors.RequireAdminResourceClient(pd.AdminClient, &resp.Diagnostics) {
		return
	}

	r.adminClient = pd.AdminClient
}

// --- Create ---

func (r *WorkspaceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data WorkspaceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body := workspaceCreateRequest{Name: data.Name.ValueString()}

	if !data.Tags.IsNull() && !data.Tags.IsUnknown() {
		tags := map[string]string{}
		resp.Diagnostics.Append(data.Tags.ElementsAs(ctx, &tags, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
		body.Tags = tags
	}
	if !data.ExternalKeyID.IsNull() && !data.ExternalKeyID.IsUnknown() {
		body.ExternalKeyID = data.ExternalKeyID.ValueString()
	}

	if !data.DataResidency.IsNull() && !data.DataResidency.IsUnknown() {
		dr, diags := buildCreateDataResidency(ctx, data.DataResidency)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		body.DataResidency = dr
	}

	respBytes, err := r.adminClient.DoRequest(ctx, "POST", "/v1/organizations/workspaces", body)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create workspace: %s", err))
		return
	}

	var ws workspaceAPIResponse
	if err := json.Unmarshal(respBytes, &ws); err != nil {
		resp.Diagnostics.AddError("Parse Error", fmt.Sprintf("Unable to parse create workspace response: %s", err))
		return
	}

	resp.Diagnostics.Append(mapWorkspaceToState(ctx, &ws, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Read ---

func (r *WorkspaceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data WorkspaceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	respBytes, err := r.adminClient.DoRequest(ctx, "GET", "/v1/organizations/workspaces/"+data.ID.ValueString(), nil)
	if err != nil {
		if admin.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read workspace: %s", err))
		return
	}

	var ws workspaceAPIResponse
	if err := json.Unmarshal(respBytes, &ws); err != nil {
		resp.Diagnostics.AddError("Parse Error", fmt.Sprintf("Unable to parse read workspace response: %s", err))
		return
	}

	// If archived externally, remove from state so Terraform recreates it.
	if ws.ArchivedAt != nil && *ws.ArchivedAt != "" {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(mapWorkspaceToState(ctx, &ws, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Update ---

func (r *WorkspaceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data WorkspaceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state WorkspaceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body := workspaceUpdateRequest{Name: data.Name.ValueString()}

	tagsPatch, diags := buildWorkspaceTagsPatch(ctx, data.Tags, state.Tags)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	body.Tags = tagsPatch

	if !data.DataResidency.IsNull() && !data.DataResidency.IsUnknown() {
		dr, diags := buildUpdateDataResidency(ctx, data.DataResidency)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		body.DataResidency = dr
	}

	respBytes, err := r.adminClient.DoRequest(ctx, "POST", "/v1/organizations/workspaces/"+state.ID.ValueString(), body)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update workspace: %s", err))
		return
	}

	var ws workspaceAPIResponse
	if err := json.Unmarshal(respBytes, &ws); err != nil {
		resp.Diagnostics.AddError("Parse Error", fmt.Sprintf("Unable to parse update workspace response: %s", err))
		return
	}

	resp.Diagnostics.Append(mapWorkspaceToState(ctx, &ws, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Delete (archive) ---

func (r *WorkspaceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data WorkspaceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.adminClient.DoRequest(ctx, "POST", "/v1/organizations/workspaces/"+data.ID.ValueString()+"/archive", nil)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to archive workspace: %s", err))
		return
	}
}

// --- ImportState ---

func (r *WorkspaceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// ============================================================================
// Helper functions
// ============================================================================

// parseAllowedInferenceGeos converts the API union type (string or []string) to a []string.
// The API string "unrestricted" becomes ["unrestricted"] in the Terraform list.
func parseAllowedInferenceGeos(raw json.RawMessage) ([]string, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	// Try string first ("unrestricted" scalar variant).
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return []string{s}, nil
	}
	// Fall back to array variant.
	var arr []string
	if err := json.Unmarshal(raw, &arr); err != nil {
		return nil, fmt.Errorf("parse allowed_inference_geos: %w", err)
	}
	return arr, nil
}

// buildAllowedInferenceGeos converts a Terraform list back to the API union type.
// ["unrestricted"] is serialized as the JSON string "unrestricted"; anything else is an array.
func buildAllowedInferenceGeos(geos []string) json.RawMessage {
	if len(geos) == 1 && geos[0] == "unrestricted" {
		return json.RawMessage(`"unrestricted"`)
	}
	b, _ := json.Marshal(geos)
	return b
}

// buildCreateDataResidency converts the Terraform data_residency object to an API create request struct.
func buildCreateDataResidency(ctx context.Context, obj types.Object) (*workspaceCreateDataResidency, diag.Diagnostics) {
	var m workspaceDataResidencyModel
	diags := obj.As(ctx, &m, basetypes.ObjectAsOptions{})
	if diags.HasError() {
		return nil, diags
	}

	dr := &workspaceCreateDataResidency{}

	if !m.WorkspaceGeo.IsNull() && !m.WorkspaceGeo.IsUnknown() {
		dr.WorkspaceGeo = m.WorkspaceGeo.ValueString()
	}
	if !m.DefaultInferenceGeo.IsNull() && !m.DefaultInferenceGeo.IsUnknown() {
		dr.DefaultInferenceGeo = m.DefaultInferenceGeo.ValueString()
	}
	if !m.AllowedInferenceGeos.IsNull() && !m.AllowedInferenceGeos.IsUnknown() {
		var geos []string
		diags.Append(m.AllowedInferenceGeos.ElementsAs(ctx, &geos, false)...)
		if !diags.HasError() {
			dr.AllowedInferenceGeos = buildAllowedInferenceGeos(geos)
		}
	}

	return dr, diags
}

// buildUpdateDataResidency converts the Terraform data_residency object to an API update request struct.
// workspace_geo is intentionally excluded: it is immutable after creation, and RequiresReplace ensures
// that Update is never called when workspace_geo changes (replacement happens instead).
func buildUpdateDataResidency(ctx context.Context, obj types.Object) (*workspaceUpdateDataResidency, diag.Diagnostics) {
	var m workspaceDataResidencyModel
	diags := obj.As(ctx, &m, basetypes.ObjectAsOptions{})
	if diags.HasError() {
		return nil, diags
	}

	dr := &workspaceUpdateDataResidency{}

	if !m.DefaultInferenceGeo.IsNull() && !m.DefaultInferenceGeo.IsUnknown() {
		dr.DefaultInferenceGeo = m.DefaultInferenceGeo.ValueString()
	}
	if !m.AllowedInferenceGeos.IsNull() && !m.AllowedInferenceGeos.IsUnknown() {
		var geos []string
		diags.Append(m.AllowedInferenceGeos.ElementsAs(ctx, &geos, false)...)
		if !diags.HasError() {
			dr.AllowedInferenceGeos = buildAllowedInferenceGeos(geos)
		}
	}

	return dr, diags
}

// mapWorkspaceToState maps an API workspace response to the Terraform state model.
func mapWorkspaceToState(ctx context.Context, ws *workspaceAPIResponse, data *WorkspaceResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics

	data.ID = types.StringValue(ws.ID)
	data.Name = types.StringValue(ws.Name)
	data.DisplayColor = types.StringValue(ws.DisplayColor)
	data.Type = types.StringValue(ws.Type)
	data.CreatedAt = types.StringValue(ws.CreatedAt)
	data.ExternalKeyID = tfvalue.StringOrNull(derefString(ws.ExternalKeyID))
	data.CompartmentID = tfvalue.StringOrNull(derefString(ws.CompartmentID))
	data.UserProfileID = tfvalue.StringOrNull(derefString(ws.UserProfileID))

	tags, d := workspaceTagsToMap(ws.Tags)
	diags.Append(d...)
	data.Tags = tags

	idr, d := workspaceRetentionToObject(ws.InferenceDataRetention)
	diags.Append(d...)
	data.InferenceDataRetention = idr

	if ws.ArchivedAt != nil && *ws.ArchivedAt != "" {
		data.ArchivedAt = types.StringValue(*ws.ArchivedAt)
	} else {
		data.ArchivedAt = types.StringNull()
	}

	// Map data_residency
	geos, err := parseAllowedInferenceGeos(ws.DataResidency.AllowedInferenceGeos)
	if err != nil {
		diags.AddError("Parse Error", fmt.Sprintf("Unable to parse allowed_inference_geos: %s", err))
		return diags
	}

	var allowedGeosList types.List
	if len(geos) == 0 {
		allowedGeosList = types.ListNull(types.StringType)
	} else {
		geoElems := make([]attr.Value, len(geos))
		for i, g := range geos {
			geoElems[i] = types.StringValue(g)
		}
		var d diag.Diagnostics
		allowedGeosList, d = types.ListValue(types.StringType, geoElems)
		diags.Append(d...)
		if diags.HasError() {
			return diags
		}
	}

	drObj, d := types.ObjectValue(workspaceDataResidencyAttrTypes, map[string]attr.Value{
		"allowed_inference_geos": allowedGeosList,
		"default_inference_geo":  types.StringValue(ws.DataResidency.DefaultInferenceGeo),
		"workspace_geo":          types.StringValue(ws.DataResidency.WorkspaceGeo),
	})
	diags.Append(d...)
	data.DataResidency = drObj

	return diags
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// workspaceTagsToMap maps the API tags to a Terraform map: a JSON null becomes a
// null map, while an empty object becomes an empty (non-null) map.
func workspaceTagsToMap(tags map[string]string) (types.Map, diag.Diagnostics) {
	if tags == nil {
		return types.MapNull(types.StringType), nil
	}
	elems := make(map[string]attr.Value, len(tags))
	for k, v := range tags {
		elems[k] = types.StringValue(v)
	}
	return types.MapValue(types.StringType, elems)
}

// workspaceRetentionToObject maps inference_data_retention; an absent value yields a null object.
func workspaceRetentionToObject(r *workspaceAPIInferenceDataRetention) (types.Object, diag.Diagnostics) {
	if r == nil {
		return types.ObjectNull(workspaceInferenceDataRetentionAttrTypes), nil
	}
	return types.ObjectValue(workspaceInferenceDataRetentionAttrTypes, map[string]attr.Value{
		"type": tfvalue.StringOrNull(r.Type),
	})
}

// buildWorkspaceTagsPatch builds the tags payload of an update. The API takes
// tags as a patch: keys planned are upserted and keys removed since the prior
// state are sent as null. It returns nil (no "tags" key at all) when the plan is
// null/unknown or equal to the state. Partial-update semantics are not spelled
// out in the public reference, so the full planned map is sent on any change.
func buildWorkspaceTagsPatch(ctx context.Context, plan, state types.Map) (map[string]any, diag.Diagnostics) {
	var diags diag.Diagnostics
	if plan.IsNull() || plan.IsUnknown() || plan.Equal(state) {
		return nil, diags
	}

	planned := map[string]string{}
	diags.Append(plan.ElementsAs(ctx, &planned, false)...)
	prior := map[string]string{}
	if !state.IsNull() && !state.IsUnknown() {
		diags.Append(state.ElementsAs(ctx, &prior, false)...)
	}
	if diags.HasError() {
		return nil, diags
	}

	patch := make(map[string]any, len(planned)+len(prior))
	for k, v := range planned {
		patch[k] = v
	}
	for k := range prior {
		if _, kept := planned[k]; !kept {
			patch[k] = nil
		}
	}
	if len(patch) == 0 {
		return nil, diags
	}
	return patch, diags
}

// noAnthropicPrefixValidator rejects tag keys starting with "anthropic" (case-insensitive).
type noAnthropicPrefixValidator struct{}

func (noAnthropicPrefixValidator) Description(context.Context) string {
	return "tag keys must not begin with `anthropic`"
}

func (v noAnthropicPrefixValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (noAnthropicPrefixValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if strings.HasPrefix(strings.ToLower(req.ConfigValue.ValueString()), "anthropic") {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid tag key",
			fmt.Sprintf("Tag key %q must not begin with \"anthropic\".", req.ConfigValue.ValueString()))
	}
}
