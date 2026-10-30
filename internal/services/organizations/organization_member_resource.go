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
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/boolplanmodifier"
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
	ID              types.String `tfsdk:"id"`
	Email           types.String `tfsdk:"email"`
	Name            types.String `tfsdk:"name"`
	Role            types.String `tfsdk:"role"`
	AddedAt         types.String `tfsdk:"added_at"`
	Type            types.String `tfsdk:"type"`
	RemoveOnDestroy types.Bool   `tfsdk:"remove_on_destroy"`
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
			"Creating this resource looks up the configured user ID and adopts that existing member; it does not create organization membership. " +
			"Adoption requires the configured role to match the member's current role; otherwise, import the member first to review the role change as an update. " +
			"Membership must first be created by inviting a user (via `anthropic_invite` or the Claude Console) and having them accept. " +
			"This resource supports changing a member's organization role and removing them from the organization. " +
			"Destroy is blocked unless `remove_on_destroy = true` has been applied. **Warning:** When enabled, removing this resource from configuration and applying, or running `terraform destroy`, irreversibly removes the user's organization membership. There is no archive or deactivate alternative. " +
			"Requires `admin_api_key` (or `ANTHROPIC_ADMIN_API_KEY`) to be configured on the provider.",
		Attributes: map[string]schema.Attribute{
			"remove_on_destroy": schema.BoolAttribute{
				Optional:            true,
				Computed:            true,
				Default:             booldefault.StaticBool(false),
				MarkdownDescription: "If `true`, destroying this resource irreversibly removes the user's organization membership. If `false`, destroy fails and the resource remains in Terraform state. There is no archive or deactivate alternative. Default: `false`. Set this to `true` and apply before destroying the resource.",
				PlanModifiers:       []planmodifier.Bool{boolplanmodifier.UseStateForUnknown()},
			},
			"id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the existing organization member to manage. Also used as the import ID.",
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
				Required: true,
				MarkdownDescription: "Organization role of the user. " + assignableRolesDescription + " " +
					"A member holding `admin` cannot be removed through the API, and one holding a Claude Enterprise administrative role can be neither changed nor removed through it. " +
					"Not checked against a list, since the accepted set depends on the organization type and a member can hold a role the API cannot assign; " +
					"the API rejects an invalid change at apply time.",
				// No OneOf: a member, imported or adopted on create, can hold admin
				// or an Enterprise administrative role, and the configuration must be
				// able to match it; only changing to such a role is impossible.
				Validators: []validator.String{
					stringvalidator.LengthAtLeast(1),
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

func (r *OrganizationMemberResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan OrganizationMemberResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	userID := plan.ID.ValueString()
	member, err := r.getMember(ctx, userID)
	if err != nil {
		if admin.IsNotFound(err) {
			resp.Diagnostics.AddError(
				"Organization Member Not Found",
				fmt.Sprintf("User %q is not a member of this organization. Create membership by sending and accepting an invite before managing the user with this resource.", userID),
			)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read organization member: %s", err))
		return
	}

	if member.Role != plan.Role.ValueString() {
		resp.Diagnostics.AddError(
			"Organization Member Role Mismatch",
			fmt.Sprintf("Organization member %q has role %q, but the configured role is %q. Adoption cannot change an existing member's role. Import the member with terraform import <resource_address> %s, then run terraform plan to review the role change as an update before applying.", userID, member.Role, plan.Role.ValueString(), userID),
		)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, mapOrganizationMemberToResourceState(member, plan.RemoveOnDestroy))...)
}

// --- Read ---

func (r *OrganizationMemberResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data OrganizationMemberResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	member, err := r.getMember(ctx, data.ID.ValueString())
	if err != nil {
		if admin.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read organization member: %s", err))
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, mapOrganizationMemberToResourceState(member, data.RemoveOnDestroy))...)
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

	resp.Diagnostics.Append(resp.State.Set(ctx, mapOrganizationMemberToResourceState(member, plan.RemoveOnDestroy))...)
}

// --- Delete ---

// Delete removes the member from the organization (only when explicitly enabled).
// This is irreversible — unlike other Admin API resources, no archive/deactivate
// alternative exists for organization membership.
func (r *OrganizationMemberResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data OrganizationMemberResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !data.RemoveOnDestroy.ValueBool() {
		resp.Diagnostics.AddError(
			"Organization Member Removal Disabled",
			"Destroy is blocked because remove_on_destroy is not true. The member has not been removed. Set remove_on_destroy = true on this resource and run terraform apply before destroying it. Removing organization membership is irreversible; there is no archive or deactivate alternative.",
		)
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

func (r *OrganizationMemberResource) getMember(ctx context.Context, id string) (organizationMemberAPIResponse, error) {
	var member organizationMemberAPIResponse
	body, err := r.client.DoRequest(ctx, "GET", "/v1/organizations/users/"+id, nil)
	if err != nil {
		return member, err
	}
	if err := json.Unmarshal(body, &member); err != nil {
		return member, fmt.Errorf("unable to parse organization member response: %w", err)
	}
	return member, nil
}

// mapOrganizationMemberToResourceState maps an API User response to the
// resource's Terraform state model.
func mapOrganizationMemberToResourceState(member organizationMemberAPIResponse, removeOnDestroy types.Bool) OrganizationMemberResourceModel {
	if removeOnDestroy.IsNull() || removeOnDestroy.IsUnknown() {
		removeOnDestroy = types.BoolValue(false)
	}
	return OrganizationMemberResourceModel{
		ID:              types.StringValue(member.ID),
		Email:           types.StringValue(member.Email),
		Name:            types.StringValue(member.Name),
		Role:            types.StringValue(member.Role),
		AddedAt:         types.StringValue(member.AddedAt),
		Type:            types.StringValue(member.Type),
		RemoveOnDestroy: removeOnDestroy,
	}
}
