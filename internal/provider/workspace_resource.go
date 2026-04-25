// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &WorkspaceResource{}
var _ resource.ResourceWithImportState = &WorkspaceResource{}

func NewWorkspaceResource() resource.Resource {
	return &WorkspaceResource{}
}

// WorkspaceResource defines the resource implementation.
type WorkspaceResource struct {
	adminClient *AdminClient
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

			// --- Computed ---
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

	pd, ok := req.ProviderData.(*ProviderData)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *ProviderData, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}

	if pd.AdminClient == nil {
		resp.Diagnostics.AddError(
			"Missing Admin API Key",
			"The anthropic_workspace resource requires an Admin API key. "+
				"Configure it via the admin_api_key provider argument or the ANTHROPIC_ADMIN_API_KEY environment variable.",
		)
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

	if !data.DataResidency.IsNull() && !data.DataResidency.IsUnknown() {
		dr, diags := buildCreateDataResidency(ctx, data.DataResidency)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		body.DataResidency = dr
	}

	respBytes, err := r.adminClient.doRequest(ctx, "POST", "/v1/organizations/workspaces", body)
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

	respBytes, err := r.adminClient.doRequest(ctx, "GET", "/v1/organizations/workspaces/"+data.ID.ValueString(), nil)
	if err != nil {
		if IsNotFound(err) {
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

	if !data.DataResidency.IsNull() && !data.DataResidency.IsUnknown() {
		dr, diags := buildUpdateDataResidency(ctx, data.DataResidency)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		body.DataResidency = dr
	}

	respBytes, err := r.adminClient.doRequest(ctx, "POST", "/v1/organizations/workspaces/"+state.ID.ValueString(), body)
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

	_, err := r.adminClient.doRequest(ctx, "POST", "/v1/organizations/workspaces/"+data.ID.ValueString()+"/archive", nil)
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
// ["unrestricted"] is serialised as the JSON string "unrestricted"; anything else is an array.
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
