// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package organizations

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
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
var _ resource.Resource = &OrganizationMemberResource{}
var _ resource.ResourceWithImportState = &OrganizationMemberResource{}

func NewOrganizationMemberResource() resource.Resource {
	return &OrganizationMemberResource{}
}

// OrganizationMemberResource defines the resource implementation.
type OrganizationMemberResource struct {
	client *admin.Client
}

// OrganizationMemberResourceModel maps the resource schema to Go types.
type OrganizationMemberResourceModel struct {
	ID      types.String `tfsdk:"id"`
	Email   types.String `tfsdk:"email"`
	Name    types.String `tfsdk:"name"`
	Role    types.String `tfsdk:"role"`
	AddedAt types.String `tfsdk:"added_at"`
	Type    types.String `tfsdk:"type"`
}

type organizationMemberUpdateRequest struct {
	Role string `json:"role"`
}

// --- Schema ---

func (r *OrganizationMemberResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization_member"
}

func (r *OrganizationMemberResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages an existing organization member (User) via the Admin API. " +
			"Organization membership is created by inviting a user (via `anthropic_invite` or the Anthropic Console) and having them accept — " +
			"use `terraform import` to bring an existing member under Terraform management. " +
			"This resource supports changing a member's organization role and removing them from the organization. " +
			"Requires `admin_api_key` (or `ANTHROPIC_ADMIN_API_KEY`) to be configured on the provider.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the user. Also used as the import ID.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"email": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Email of the user.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Name of the user.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"role": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Organization role of the user. Valid values: `user`, `developer`, `billing`, `admin`, `claude_code_user`.",
				Validators: []validator.String{
					stringvalidator.OneOf("user", "developer", "billing", "admin", "claude_code_user"),
				},
			},
			"added_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the user joined the organization.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"type": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Object type. Always `user`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

// --- Configure ---

func (r *OrganizationMemberResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

	r.client = pd.AdminClient
}

// --- Create ---

func (r *OrganizationMemberResource) Create(_ context.Context, _ resource.CreateRequest, resp *resource.CreateResponse) {
	resp.Diagnostics.AddError(
		"Organization Member Creation Not Supported",
		"Organization membership is created by inviting a user (via `anthropic_invite` or the Anthropic Console) and having them accept the invite. "+
			"Use `terraform import` to manage an existing member:\n\n"+
			"  terraform import anthropic_organization_member.<name> <user_id>",
	)
}

// --- Read ---

func (r *OrganizationMemberResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data OrganizationMemberResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	respBytes, err := r.client.DoRequest(ctx, "GET", "/v1/organizations/users/"+data.ID.ValueString(), nil)
	if err != nil {
		if admin.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read organization member: %s", err))
		return
	}

	var member organizationMemberAPIResponse
	if err := json.Unmarshal(respBytes, &member); err != nil {
		resp.Diagnostics.AddError("Parse Error", fmt.Sprintf("Unable to parse organization member response: %s", err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, mapOrganizationMemberToResourceState(member))...)
}

// --- Update ---

func (r *OrganizationMemberResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan OrganizationMemberResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body := organizationMemberUpdateRequest{Role: plan.Role.ValueString()}

	respBytes, err := r.client.DoRequest(ctx, "POST", "/v1/organizations/users/"+plan.ID.ValueString(), body)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update organization member: %s", err))
		return
	}

	var member organizationMemberAPIResponse
	if err := json.Unmarshal(respBytes, &member); err != nil {
		resp.Diagnostics.AddError("Parse Error", fmt.Sprintf("Unable to parse update organization member response: %s", err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, mapOrganizationMemberToResourceState(member))...)
}

// --- Delete ---

// Delete removes the member from the organization. This is irreversible —
// unlike other Admin API resources, no archive/deactivate alternative exists
// for organization membership.
func (r *OrganizationMemberResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data OrganizationMemberResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.DoRequest(ctx, "DELETE", "/v1/organizations/users/"+data.ID.ValueString(), nil)
	if err != nil && !admin.IsNotFound(err) {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to remove organization member: %s", err))
		return
	}
}

// --- ImportState ---

func (r *OrganizationMemberResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// ============================================================================
// Helper functions
// ============================================================================

// mapOrganizationMemberToResourceState maps an API User response to the
// resource's Terraform state model.
func mapOrganizationMemberToResourceState(member organizationMemberAPIResponse) OrganizationMemberResourceModel {
	return OrganizationMemberResourceModel{
		ID:      types.StringValue(member.ID),
		Email:   types.StringValue(member.Email),
		Name:    types.StringValue(member.Name),
		Role:    types.StringValue(member.Role),
		AddedAt: types.StringValue(member.AddedAt),
		Type:    types.StringValue(member.Type),
	}
}
