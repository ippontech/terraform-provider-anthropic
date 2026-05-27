// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package apikeys

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
	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &APIKeyResource{}
var _ resource.ResourceWithImportState = &APIKeyResource{}

func NewAPIKeyResource() resource.Resource {
	return &APIKeyResource{}
}

// APIKeyResource defines the resource implementation.
type APIKeyResource struct {
	adminClient *admin.Client
}

// --- Terraform data models ---

type APIKeyResourceModel struct {
	ID             types.String `tfsdk:"id"`
	Name           types.String `tfsdk:"name"`
	Status         types.String `tfsdk:"status"`
	WorkspaceID    types.String `tfsdk:"workspace_id"`
	CreatedAt      types.String `tfsdk:"created_at"`
	CreatedBy      types.Object `tfsdk:"created_by"`
	PartialKeyHint types.String `tfsdk:"partial_key_hint"`
	Type           types.String `tfsdk:"type"`
}

var apiKeyCreatedByAttrTypes = map[string]attr.Type{
	"id":   types.StringType,
	"type": types.StringType,
}

// --- Admin API request/response types ---

type apiKeyAPIResponse struct {
	ID             string          `json:"id"`
	Name           string          `json:"name"`
	Status         string          `json:"status"`
	WorkspaceID    *string         `json:"workspace_id"`
	CreatedAt      string          `json:"created_at"`
	CreatedBy      apiKeyCreatedBy `json:"created_by"`
	PartialKeyHint string          `json:"partial_key_hint"`
	Type           string          `json:"type"`
}

type apiKeyCreatedBy struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

type apiKeyUpdateRequest struct {
	Name   string `json:"name,omitempty"`
	Status string `json:"status,omitempty"`
}

// --- Schema ---

func (r *APIKeyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_api_key"
}

func (r *APIKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages an existing Anthropic API key via the Admin API. " +
			"API keys must be created through the Anthropic Console — use `terraform import` to bring an existing key under Terraform management. " +
			"This resource supports renaming and deactivating existing keys. " +
			"Requires `admin_api_key` (or `ANTHROPIC_ADMIN_API_KEY`) to be configured on the provider.\n\n" +
			"> **Security note:** This resource manages API keys but never exposes the key material. " +
			"The actual secret is only visible in the Anthropic Console at creation time. " +
			"Use this resource for lifecycle management of existing keys (rename, deactivate) only.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Unique API key identifier. Set automatically on import.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Human-readable name of the API key. Can be changed after import.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"status": schema.StringAttribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "Status of the API key. Valid values: `active`, `inactive`. " +
					"Setting to `inactive` deactivates the key. " +
					"Deleting this resource sets the status to `inactive` and removes it from state.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"workspace_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The ID of the workspace this key is scoped to, or null for organization-level keys.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the API key was created.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"created_by": schema.SingleNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Details about the actor who created this API key.",
				PlanModifiers:       []planmodifier.Object{objectplanmodifier.UseStateForUnknown()},
				Attributes: map[string]schema.Attribute{
					"id": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "Identifier of the creator.",
					},
					"type": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "Type of the creator (e.g. `user`).",
					},
				},
			},
			"partial_key_hint": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The last four visible characters of the API key, for identification.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"type": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Object type. Always `api_key`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

// --- Configure ---

func (r *APIKeyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *APIKeyResource) Create(_ context.Context, _ resource.CreateRequest, resp *resource.CreateResponse) {
	resp.Diagnostics.AddError(
		"API Key Creation Not Supported",
		"API keys must be created via the Anthropic Console. "+
			"Use `terraform import` to manage an existing key:\n\n"+
			"  terraform import anthropic_api_key.<name> <api_key_id>",
	)
}

// --- Read ---

func (r *APIKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data APIKeyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	respBytes, err := r.adminClient.DoRequest(ctx, "GET", "/v1/organizations/api_keys/"+data.ID.ValueString(), nil)
	if err != nil {
		if admin.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read API key: %s", err))
		return
	}

	var key apiKeyAPIResponse
	if err := json.Unmarshal(respBytes, &key); err != nil {
		resp.Diagnostics.AddError("Parse Error", fmt.Sprintf("Unable to parse read API key response: %s", err))
		return
	}

	resp.Diagnostics.Append(mapAPIKeyToState(ctx, &key, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Update ---

func (r *APIKeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan APIKeyResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state APIKeyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body := apiKeyUpdateRequest{
		Name:   plan.Name.ValueString(),
		Status: plan.Status.ValueString(),
	}

	respBytes, err := r.adminClient.DoRequest(ctx, "POST", "/v1/organizations/api_keys/"+state.ID.ValueString(), body)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update API key: %s", err))
		return
	}

	var key apiKeyAPIResponse
	if err := json.Unmarshal(respBytes, &key); err != nil {
		resp.Diagnostics.AddError("Parse Error", fmt.Sprintf("Unable to parse update API key response: %s", err))
		return
	}

	resp.Diagnostics.Append(mapAPIKeyToState(ctx, &key, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// --- Delete (deactivate) ---

func (r *APIKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data APIKeyResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body := apiKeyUpdateRequest{Status: "inactive"}
	_, err := r.adminClient.DoRequest(ctx, "POST", "/v1/organizations/api_keys/"+data.ID.ValueString(), body)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to deactivate API key: %s", err))
		return
	}
}

// --- ImportState ---

func (r *APIKeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// ============================================================================
// Helper functions
// ============================================================================

// mapAPIKeyToState maps an API key response to the Terraform state model.
func mapAPIKeyToState(_ context.Context, key *apiKeyAPIResponse, data *APIKeyResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics

	data.ID = types.StringValue(key.ID)
	data.Name = types.StringValue(key.Name)
	data.Status = types.StringValue(key.Status)
	data.CreatedAt = types.StringValue(key.CreatedAt)
	data.PartialKeyHint = types.StringValue(key.PartialKeyHint)
	data.Type = types.StringValue(key.Type)

	if key.WorkspaceID != nil {
		data.WorkspaceID = types.StringValue(*key.WorkspaceID)
	} else {
		data.WorkspaceID = types.StringNull()
	}

	createdByObj, d := types.ObjectValue(apiKeyCreatedByAttrTypes, map[string]attr.Value{
		"id":   types.StringValue(key.CreatedBy.ID),
		"type": types.StringValue(key.CreatedBy.Type),
	})
	diags.Append(d...)
	if diags.HasError() {
		return diags
	}
	data.CreatedBy = createdByObj

	return diags
}
