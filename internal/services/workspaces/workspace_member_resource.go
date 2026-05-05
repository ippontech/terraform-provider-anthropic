// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &WorkspaceMemberResource{}
var _ resource.ResourceWithImportState = &WorkspaceMemberResource{}

func NewWorkspaceMemberResource() resource.Resource {
	return &WorkspaceMemberResource{}
}

// WorkspaceMemberResource defines the resource implementation.
type WorkspaceMemberResource struct {
	adminClient *admin.Client
}

// --- Terraform data models ---

type WorkspaceMemberResourceModel struct {
	ID            types.String `tfsdk:"id"`
	WorkspaceID   types.String `tfsdk:"workspace_id"`
	UserID        types.String `tfsdk:"user_id"`
	WorkspaceRole types.String `tfsdk:"workspace_role"`
	Type          types.String `tfsdk:"type"`
}

// --- Admin API request/response types ---

type workspaceMemberAPIResponse struct {
	UserID        string `json:"user_id"`
	WorkspaceRole string `json:"workspace_role"`
	Type          string `json:"type"`
}

type workspaceMemberCreateRequest struct {
	UserID        string `json:"user_id"`
	WorkspaceRole string `json:"workspace_role"`
}

type workspaceMemberUpdateRequest struct {
	WorkspaceRole string `json:"workspace_role"`
}

// --- Schema validator ---

// noWorkspaceBillingValidator rejects the "workspace_billing" role.
type noWorkspaceBillingValidator struct{}

func (v noWorkspaceBillingValidator) Description(_ context.Context) string {
	return "workspace_billing role cannot be assigned via this resource"
}

func (v noWorkspaceBillingValidator) MarkdownDescription(_ context.Context) string {
	return "The `workspace_billing` role cannot be assigned via this resource."
}

func (v noWorkspaceBillingValidator) ValidateString(_ context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	if req.ConfigValue.ValueString() == "workspace_billing" {
		resp.Diagnostics.AddAttributeError(
			req.Path,
			"Invalid workspace_role",
			"The workspace_billing role cannot be assigned via this resource. "+
				"Use the Anthropic Console to assign billing roles.",
		)
	}
}

// --- Schema ---

func (r *WorkspaceMemberResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_workspace_member"
}

func (r *WorkspaceMemberResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Assigns a user to an Anthropic Workspace with a given role via the Admin API. " +
			"Requires `admin_api_key` (or `ANTHROPIC_ADMIN_API_KEY`) to be configured on the provider.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Composite identifier in the form `<workspace_id>:<user_id>`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"workspace_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the workspace to assign the user to.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"user_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the user to assign to the workspace.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"workspace_role": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "The role to assign to the user in the workspace. " +
					"Valid values: `workspace_user`, `workspace_developer`, `workspace_restricted_developer`, " +
					"`workspace_admin`. Note: `workspace_billing` cannot be assigned via this resource.",
				Validators: []validator.String{
					noWorkspaceBillingValidator{},
				},
			},
			"type": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Object type. Always `workspace_member`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

// --- Configure ---

func (r *WorkspaceMemberResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *WorkspaceMemberResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data WorkspaceMemberResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Double-check billing role (validator already catches it at plan time, but belt-and-suspenders).
	if data.WorkspaceRole.ValueString() == "workspace_billing" {
		resp.Diagnostics.AddError(
			"Invalid workspace_role",
			"The workspace_billing role cannot be assigned via this resource.",
		)
		return
	}

	body := workspaceMemberCreateRequest{
		UserID:        data.UserID.ValueString(),
		WorkspaceRole: data.WorkspaceRole.ValueString(),
	}

	url := fmt.Sprintf("/v1/organizations/workspaces/%s/members", data.WorkspaceID.ValueString())
	respBytes, err := r.adminClient.DoRequest(ctx, "POST", url, body)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create workspace member: %s", err))
		return
	}

	var member workspaceMemberAPIResponse
	if err := json.Unmarshal(respBytes, &member); err != nil {
		resp.Diagnostics.AddError("Parse Error", fmt.Sprintf("Unable to parse create workspace member response: %s", err))
		return
	}

	data.ID = types.StringValue(data.WorkspaceID.ValueString() + ":" + data.UserID.ValueString())
	data.WorkspaceRole = types.StringValue(member.WorkspaceRole)
	data.Type = types.StringValue(member.Type)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Read ---

func (r *WorkspaceMemberResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data WorkspaceMemberResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	url := fmt.Sprintf("/v1/organizations/workspaces/%s/members/%s",
		data.WorkspaceID.ValueString(), data.UserID.ValueString())

	respBytes, err := r.adminClient.DoRequest(ctx, "GET", url, nil)
	if err != nil {
		if admin.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read workspace member: %s", err))
		return
	}

	var member workspaceMemberAPIResponse
	if err := json.Unmarshal(respBytes, &member); err != nil {
		resp.Diagnostics.AddError("Parse Error", fmt.Sprintf("Unable to parse read workspace member response: %s", err))
		return
	}

	data.WorkspaceRole = types.StringValue(member.WorkspaceRole)
	data.Type = types.StringValue(member.Type)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Update ---

func (r *WorkspaceMemberResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data WorkspaceMemberResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state WorkspaceMemberResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body := workspaceMemberUpdateRequest{
		WorkspaceRole: data.WorkspaceRole.ValueString(),
	}

	url := fmt.Sprintf("/v1/organizations/workspaces/%s/members/%s",
		state.WorkspaceID.ValueString(), state.UserID.ValueString())

	respBytes, err := r.adminClient.DoRequest(ctx, "POST", url, body)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update workspace member: %s", err))
		return
	}

	var member workspaceMemberAPIResponse
	if err := json.Unmarshal(respBytes, &member); err != nil {
		resp.Diagnostics.AddError("Parse Error", fmt.Sprintf("Unable to parse update workspace member response: %s", err))
		return
	}

	data.ID = state.ID
	data.WorkspaceID = state.WorkspaceID
	data.UserID = state.UserID
	data.WorkspaceRole = types.StringValue(member.WorkspaceRole)
	data.Type = types.StringValue(member.Type)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Delete ---

func (r *WorkspaceMemberResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data WorkspaceMemberResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	url := fmt.Sprintf("/v1/organizations/workspaces/%s/members/%s",
		data.WorkspaceID.ValueString(), data.UserID.ValueString())

	_, err := r.adminClient.DoRequest(ctx, "DELETE", url, nil)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete workspace member: %s", err))
		return
	}
}

// --- ImportState ---

func (r *WorkspaceMemberResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.SplitN(req.ID, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		resp.Diagnostics.AddError(
			"Invalid import ID",
			"Expected format: <workspace_id>:<user_id>",
		)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("workspace_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("user_id"), parts[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}
