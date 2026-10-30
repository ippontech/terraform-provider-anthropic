// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package rbac

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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
	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &RBACGroupMemberResource{}
var _ resource.ResourceWithImportState = &RBACGroupMemberResource{}

func NewRBACGroupMemberResource() resource.Resource {
	return &RBACGroupMemberResource{}
}

// RBACGroupMemberResource adds a user to a Claude Enterprise RBAC group. The
// membership has no update endpoint and no single-member GET: Read pages the
// group's member list.
type RBACGroupMemberResource struct {
	client *admin.Client
}

// RBACGroupMemberResourceModel describes the resource data model.
type RBACGroupMemberResourceModel struct {
	ID          types.String `tfsdk:"id"`
	RBACGroupID types.String `tfsdk:"rbac_group_id"`
	UserID      types.String `tfsdk:"user_id"`
	Email       types.String `tfsdk:"email"`
	CreatedAt   types.String `tfsdk:"created_at"`
}

type rbacGroupMemberAddRequest struct {
	UserID string `json:"user_id"`
}

// --- Schema ---

func (r *RBACGroupMemberResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_rbac_group_member"
}

func (r *RBACGroupMemberResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Adds a user to an RBAC group of the Claude Enterprise organization via the Admin API, granting them the permissions of the group's custom roles. " +
			"The membership of a group provisioned by the identity provider (`source_type = \"scim\"`) cannot be changed through the API while SCIM provisioning is on. " +
			"The key must be created for all linked organizations and carry the `read:rbac_groups` and `write:rbac_groups` scopes. " +
			enterpriseKeyNote,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Composite identifier in the form `<rbac_group_id>:<user_id>`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"rbac_group_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "ID of the RBAC group (`rbac_group_...`). Changing it forces replacement.",
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"user_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "ID of the user to add (`user_...`). Changing it forces replacement.",
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"email": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Email of the user.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the user was added to the group.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

// --- Configure ---

func (r *RBACGroupMemberResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *RBACGroupMemberResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data RBACGroupMemberResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	groupID := data.RBACGroupID.ValueString()
	body, err := r.client.DoRequest(ctx, "POST", rbacURL(rbacGroupMembersPath(groupID), nil), rbacGroupMemberAddRequest{UserID: data.UserID.ValueString()})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to add user to RBAC group: %s", err))
		return
	}

	var member rbacGroupMemberAPIResponse
	if err := json.Unmarshal(body, &member); err != nil {
		resp.Diagnostics.AddError("Parse Error", fmt.Sprintf("Unable to parse add RBAC group member response: %s", err))
		return
	}

	mapRBACGroupMemberToState(member, &data)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Read ---

// Read lists the group's members and looks for the tracked user. A user
// missing from the list, or a 404 on the group itself, drops the resource from
// state so the next plan adds the membership again.
func (r *RBACGroupMemberResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data RBACGroupMemberResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	member, err := findRBACGroupMember(ctx, r.client, data.RBACGroupID.ValueString(), data.UserID.ValueString())
	if err != nil {
		if admin.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list RBAC group members: %s", err))
		return
	}
	if member == nil {
		resp.State.RemoveResource(ctx)
		return
	}

	mapRBACGroupMemberToState(*member, &data)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Update ---

// Update is a no-op: every attribute is either RequiresReplace or Computed,
// and the API has no update endpoint for a membership.
func (r *RBACGroupMemberResource) Update(_ context.Context, _ resource.UpdateRequest, _ *resource.UpdateResponse) {
}

// --- Delete ---

// Delete removes the user from the group (a hard remove). A 404 means the
// membership or the group is already gone.
func (r *RBACGroupMemberResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data RBACGroupMemberResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.DoRequest(ctx, "DELETE", rbacURL(rbacGroupMemberPath(data.RBACGroupID.ValueString(), data.UserID.ValueString()), nil), nil)
	if err != nil && !admin.IsNotFound(err) {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to remove user from RBAC group: %s", err))
	}
}

// --- ImportState ---

func (r *RBACGroupMemberResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.SplitN(req.ID, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		resp.Diagnostics.AddError(
			"Invalid Import ID",
			"Expected format: <rbac_group_id>:<user_id>",
		)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("rbac_group_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("user_id"), parts[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

// ============================================================================
// Helper functions
// ============================================================================

// findRBACGroupMember pages through the group's members and returns the one
// with userID, or nil when the user is not in the group. It stops at the page
// that holds the user.
func findRBACGroupMember(ctx context.Context, client *admin.Client, groupID, userID string) (*rbacGroupMemberAPIResponse, error) {
	var found *rbacGroupMemberAPIResponse
	err := listPages(ctx, client, rbacGroupMembersPath(groupID), func(members []rbacGroupMemberAPIResponse) bool {
		for i := range members {
			if members[i].UserID == userID {
				found = &members[i]
				return false
			}
		}
		return true
	})
	if err != nil {
		return nil, err
	}
	return found, nil
}

// mapRBACGroupMemberToState maps an API membership to the resource model. The
// group and user IDs stay as configured (or imported): they are the request's
// path and body, so the response's own copies (`rbac_group_id`, and its
// deprecated alias `group_id` under 2023-06-01) carry nothing new.
func mapRBACGroupMemberToState(member rbacGroupMemberAPIResponse, data *RBACGroupMemberResourceModel) {
	data.ID = types.StringValue(data.RBACGroupID.ValueString() + ":" + data.UserID.ValueString())
	data.Email = tfvalue.StringOrNull(member.Email)
	data.CreatedAt = tfvalue.StringOrNull(member.CreatedAt)
}
