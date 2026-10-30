// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package organizations

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/setplanmodifier"
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
	ID           types.String `tfsdk:"id"`
	Email        types.String `tfsdk:"email"`
	Role         types.String `tfsdk:"role"`
	RBACGroupIDs types.Set    `tfsdk:"rbac_group_ids"`
	Status       types.String `tfsdk:"status"`
	InvitedAt    types.String `tfsdk:"invited_at"`
	ExpiresAt    types.String `tfsdk:"expires_at"`
	AcceptedAt   types.String `tfsdk:"accepted_at"`
	Type         types.String `tfsdk:"type"`
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
	// RBACGroupIDs is nil when the field is absent or null, and points to an
	// empty slice when the API records no group (`[]`, what the reference
	// documents for an invite without groups).
	RBACGroupIDs *[]string `json:"rbac_group_ids"`
}

type inviteCreateRequest struct {
	Email        string   `json:"email"`
	Role         string   `json:"role"`
	RBACGroupIDs []string `json:"rbac_group_ids,omitempty"`
}

// rbacGroupIDPattern matches an RBAC group ID, like the provider's
// workspace_id pattern matches a workspace ID.
var rbacGroupIDPattern = regexp.MustCompile(`^rbac_group_[0-9A-Za-z]+$`)

// maxInviteRBACGroups is the API's limit on rbac_group_ids (maxItems).
const maxInviteRBACGroups = 100

// --- Schema ---

func (r *InviteResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_invite"
}

func (r *InviteResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Creates and manages an invitation for a user to join the organization via the Admin API. " +
			"Requires `admin_api_key` (or `ANTHROPIC_ADMIN_API_KEY`) to be configured on the provider. " +
			"There is no update operation: changing `email`, `role` or `rbac_group_ids` replaces the invite. " +
			"The resource remains managed after the invite is accepted or expires; deleting this resource cancels the invite when possible.",
		Attributes: map[string]schema.Attribute{
			// --- Writable ---
			"email": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Email address of the person to invite. Immutable after creation — changing this forces a new resource.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"role": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Organization role to grant once the invite is accepted. The valid set depends on the organization's " +
					"type: Console organizations use `user`, `developer`, `billing`, `admin`, `claude_code_user`; Enterprise organizations " +
					"use `user`, `managed`, `owner`, `membership_admin`, `primary_owner`. Not validated against a fixed list here since the " +
					"valid set is organization-type-dependent; an invalid value is rejected by the API at apply time. " +
					"Immutable after creation — changing this forces a new resource.",
				Validators:    []validator.String{stringvalidator.LengthAtLeast(1)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"rbac_group_ids": schema.SetAttribute{
				Optional:    true,
				ElementType: types.StringType,
				MarkdownDescription: "IDs of the RBAC groups (`rbac_group_...`, e.g. `anthropic_rbac_group.example.id`) the person joins when they accept the invite, " +
					"granting them the permissions of the custom roles attached to those groups. At most 100. " +
					"Claude Enterprise only: a non-empty set requires a Claude Enterprise scoped Admin API key, created for all linked organizations, " +
					"carrying `write:rbac_groups` in addition to `write:members`. " +
					"The membership of a group provisioned by the identity provider (`source_type = \"scim\"`) is managed in the identity provider. " +
					"Read back from the API on refresh. Changing it forces a new resource (a new invitation email), " +
					"except switching between unset and an empty set, which both mean no group.",
				Validators: []validator.Set{
					setvalidator.SizeAtMost(maxInviteRBACGroups),
					setvalidator.ValueStringsAre(stringvalidator.RegexMatches(rbacGroupIDPattern, "must be an RBAC group ID (`rbac_group_` followed by letters and digits)")),
				},
				PlanModifiers: []planmodifier.Set{rbacGroupIDsRequiresReplace()},
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

	body, diags := buildInviteCreateRequest(ctx, data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	respBytes, err := r.adminClient.DoRequest(ctx, "POST", "/v1/organizations/invites?beta=true", body)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create invite: %s", err))
		return
	}

	var inv inviteAPIResponse
	if err := json.Unmarshal(respBytes, &inv); err != nil {
		resp.Diagnostics.AddError("Parse Error", fmt.Sprintf("Unable to parse create invite response: %s", err))
		return
	}

	resp.Diagnostics.Append(mapInviteToState(&inv, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Read ---

func (r *InviteResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data InviteResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	respBytes, err := r.adminClient.DoRequest(ctx, "GET", "/v1/organizations/invites/"+data.ID.ValueString()+"?beta=true", nil)
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

	if inv.Status == "deleted" {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(mapInviteToState(&inv, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Update ---

// Update is reached only when rbac_group_ids switches between unset and an
// empty set: every other change to an input replaces the invite, since the
// Admin API has no update endpoint for invites. Both values send no group, so
// there is nothing to send: the prior state is kept with the planned value.
func (r *InviteResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state InviteResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	state.RBACGroupIDs = plan.RBACGroupIDs
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// --- Delete ---

func (r *InviteResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data InviteResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if !data.Status.IsNull() && !data.Status.IsUnknown() && data.Status.ValueString() != "pending" {
		return
	}

	_, err := r.adminClient.DoRequest(ctx, "DELETE", "/v1/organizations/invites/"+data.ID.ValueString()+"?beta=true", nil)
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

// buildInviteCreateRequest builds the create body from the plan. A null or
// empty rbac_group_ids sends no `rbac_group_ids` at all, so a Console
// organization or a key without `write:rbac_groups` is not affected. The IDs
// are sorted so the body does not depend on set order.
func buildInviteCreateRequest(ctx context.Context, data InviteResourceModel) (inviteCreateRequest, diag.Diagnostics) {
	body := inviteCreateRequest{
		Email: data.Email.ValueString(),
		Role:  data.Role.ValueString(),
	}

	var diags diag.Diagnostics
	if !data.RBACGroupIDs.IsNull() && !data.RBACGroupIDs.IsUnknown() {
		diags.Append(data.RBACGroupIDs.ElementsAs(ctx, &body.RBACGroupIDs, false)...)
		sort.Strings(body.RBACGroupIDs)
	}
	return body, diags
}

// mapInviteToState maps an API invite response to the Terraform state model.
// data holds the planned (Create) or stored (Read) values on entry, which
// rbac_group_ids is mapped against.
func mapInviteToState(inv *inviteAPIResponse, data *InviteResourceModel) diag.Diagnostics {
	data.ID = types.StringValue(inv.ID)
	data.Email = types.StringValue(inv.Email)
	data.Role = types.StringValue(inv.Role)
	data.Status = types.StringValue(inv.Status)
	data.InvitedAt = types.StringValue(inv.InvitedAt)
	data.ExpiresAt = types.StringValue(inv.ExpiresAt)
	data.AcceptedAt = types.StringValue(inv.AcceptedAt)
	data.Type = types.StringValue(inv.Type)

	var diags diag.Diagnostics
	data.RBACGroupIDs, diags = mapInviteRBACGroupIDsToState(inv.RBACGroupIDs, data.RBACGroupIDs)
	return diags
}

// mapInviteRBACGroupIDsToState maps the invite's rbac_group_ids against prior,
// the planned or stored value:
//   - absent or null (the API said nothing): prior is kept;
//   - `[]` (no group): prior is kept when it is null, so an unset attribute
//     stays unset, and is an empty set otherwise (an empty set stays empty,
//     groups that disappeared show as drift);
//   - any group: the API's set, so an out-of-band difference shows as drift.
func mapInviteRBACGroupIDsToState(ids *[]string, prior types.Set) (types.Set, diag.Diagnostics) {
	if ids == nil || (len(*ids) == 0 && prior.IsNull()) {
		return prior, nil
	}
	return inviteRBACGroupIDsValue(ids)
}

// inviteRBACGroupIDsValue maps the invite's rbac_group_ids as the API sent
// them: null when absent or null, a set otherwise. The data sources use it
// as is.
func inviteRBACGroupIDsValue(ids *[]string) (types.Set, diag.Diagnostics) {
	if ids == nil {
		return types.SetNull(types.StringType), nil
	}
	values := make([]attr.Value, len(*ids))
	for i, id := range *ids {
		values[i] = types.StringValue(id)
	}
	return types.SetValue(types.StringType, values)
}

// rbacGroupIDsRequiresReplace replaces the invite when its groups change, but
// not when rbac_group_ids only switches between unset and an empty set: both
// send no group, so replacing would only send a second invitation email. An
// unknown plan value may hold groups, so it replaces.
func rbacGroupIDsRequiresReplace() planmodifier.Set {
	return setplanmodifier.RequiresReplaceIf(
		func(_ context.Context, req planmodifier.SetRequest, resp *setplanmodifier.RequiresReplaceIfFuncResponse) {
			resp.RequiresReplace = !isEmptySet(req.StateValue) || !isEmptySet(req.PlanValue)
		},
		"Changing the RBAC groups forces replacement; switching between unset and an empty set does not.",
		"Changing the RBAC groups forces replacement; switching between unset and an empty set does not.",
	)
}

// isEmptySet reports whether v is null or a known set with no element.
func isEmptySet(v types.Set) bool {
	return v.IsNull() || (!v.IsUnknown() && len(v.Elements()) == 0)
}
