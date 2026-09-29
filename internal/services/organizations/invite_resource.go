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
var _ resource.Resource = &InviteResource{}
var _ resource.ResourceWithImportState = &InviteResource{}

func NewInviteResource() resource.Resource {
	return &InviteResource{}
}

// InviteResource defines the resource implementation.
type InviteResource struct {
	adminClient *admin.Client
}

// --- Terraform data model ---

type InviteResourceModel struct {
	ID         types.String `tfsdk:"id"`
	Email      types.String `tfsdk:"email"`
	Role       types.String `tfsdk:"role"`
	Status     types.String `tfsdk:"status"`
	InvitedAt  types.String `tfsdk:"invited_at"`
	ExpiresAt  types.String `tfsdk:"expires_at"`
	AcceptedAt types.String `tfsdk:"accepted_at"`
	Type       types.String `tfsdk:"type"`
}

// --- Admin API request/response types ---

type inviteAPIResponse struct {
	ID         string `json:"id"`
	Email      string `json:"email"`
	Role       string `json:"role"`
	Status     string `json:"status"`
	InvitedAt  string `json:"invited_at"`
	ExpiresAt  string `json:"expires_at"`
	AcceptedAt string `json:"accepted_at"`
	Type       string `json:"type"`
}

type inviteCreateRequest struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}

// organizationRoles mirrors the roles accepted by the Admin API for organization-level assignments.
var organizationRoles = []string{"user", "developer", "billing", "admin", "claude_code_user"}

// --- Schema ---

func (r *InviteResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_invite"
}

func (r *InviteResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Creates and manages an invitation for a user to join the organization via the Admin API. " +
			"Requires `admin_api_key` (or `ANTHROPIC_ADMIN_API_KEY`) to be configured on the provider. " +
			"There is no update operation: changing `email` or `role` replaces the invite. " +
			"Deleting this resource cancels a pending invite.",
		Attributes: map[string]schema.Attribute{
			// --- Writable ---
			"email": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Email address of the person to invite. Immutable after creation — changing this forces a new resource.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"role": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Organization role to grant once the invite is accepted. One of `user`, `developer`, `billing`, `admin`, `claude_code_user`. " +
					"Immutable after creation — changing this forces a new resource.",
				Validators:    []validator.String{stringvalidator.OneOf(organizationRoles...)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},

			// --- Computed ---
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Unique invite identifier assigned by the API.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"status": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Status of the invite: `pending`, `accepted`, `expired`, or `deleted`.",
			},
			"invited_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the invite was sent.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"expires_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the invite expires.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"accepted_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the invite was accepted, or empty if it has not been accepted.",
			},
			"type": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Object type. Always `invite`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

// --- Configure ---

func (r *InviteResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *InviteResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data InviteResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body := inviteCreateRequest{
		Email: data.Email.ValueString(),
		Role:  data.Role.ValueString(),
	}

	respBytes, err := r.adminClient.DoRequest(ctx, "POST", "/v1/organizations/invites", body)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create invite: %s", err))
		return
	}

	var inv inviteAPIResponse
	if err := json.Unmarshal(respBytes, &inv); err != nil {
		resp.Diagnostics.AddError("Parse Error", fmt.Sprintf("Unable to parse create invite response: %s", err))
		return
	}

	mapInviteToState(&inv, &data)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Read ---

func (r *InviteResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data InviteResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	respBytes, err := r.adminClient.DoRequest(ctx, "GET", "/v1/organizations/invites/"+data.ID.ValueString(), nil)
	if err != nil {
		if admin.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read invite: %s", err))
		return
	}

	var inv inviteAPIResponse
	if err := json.Unmarshal(respBytes, &inv); err != nil {
		resp.Diagnostics.AddError("Parse Error", fmt.Sprintf("Unable to parse read invite response: %s", err))
		return
	}

	// An invite that has been accepted, has expired, or was deleted is no longer a
	// pending invitation Terraform can manage: drop it from state so the next apply
	// re-creates it, mirroring the drift-detection pattern used elsewhere in this repo
	// (e.g. service_account_workspace skipping implicit entries).
	if inv.Status != "pending" {
		resp.State.RemoveResource(ctx)
		return
	}

	mapInviteToState(&inv, &data)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Update ---

func (r *InviteResource) Update(_ context.Context, _ resource.UpdateRequest, _ *resource.UpdateResponse) {
	// Never called: both input attributes (email, role) have RequiresReplace plan
	// modifiers, and the Admin API has no update endpoint for invites.
}

// --- Delete ---

func (r *InviteResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data InviteResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.adminClient.DoRequest(ctx, "DELETE", "/v1/organizations/invites/"+data.ID.ValueString(), nil)
	if err != nil {
		if admin.IsNotFound(err) {
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete invite: %s", err))
		return
	}
}

// --- ImportState ---

func (r *InviteResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// ============================================================================
// Helper functions
// ============================================================================

// mapInviteToState maps an API invite response to the Terraform state model.
func mapInviteToState(inv *inviteAPIResponse, data *InviteResourceModel) {
	data.ID = types.StringValue(inv.ID)
	data.Email = types.StringValue(inv.Email)
	data.Role = types.StringValue(inv.Role)
	data.Status = types.StringValue(inv.Status)
	data.InvitedAt = types.StringValue(inv.InvitedAt)
	data.ExpiresAt = types.StringValue(inv.ExpiresAt)
	data.AcceptedAt = types.StringValue(inv.AcceptedAt)
	data.Type = types.StringValue(inv.Type)
}
