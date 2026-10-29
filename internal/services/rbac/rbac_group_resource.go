// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package rbac

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &RBACGroupResource{}
var _ resource.ResourceWithImportState = &RBACGroupResource{}
var _ resource.ResourceWithModifyPlan = &RBACGroupResource{}

func NewRBACGroupResource() resource.Resource {
	return &RBACGroupResource{}
}

// RBACGroupResource manages a Claude Enterprise RBAC group. Only `name` is
// writable: roles are attached to groups in the claude.ai admin settings and
// are read-only on the API, so `role_ids` is Computed.
type RBACGroupResource struct {
	client *admin.Client
}

// rbacGroupWriteRequest is the body of both create and update: `name` is the
// only field either endpoint accepts.
type rbacGroupWriteRequest struct {
	Name string `json:"name"`
}

// --- Schema ---

func (r *RBACGroupResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_rbac_group"
}

func (r *RBACGroupResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages an RBAC group of the Claude Enterprise organization via the Admin API. Groups connect members to custom roles; " +
			"the roles attached to a group are managed in the claude.ai admin settings and are read-only here (`role_ids`). " +
			"Groups are enterprise-wide, so the key must be created for all linked organizations and carry the `read:rbac_groups` and `write:rbac_groups` scopes. " +
			enterpriseKeyNote,
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "ID of the RBAC group (`rbac_group_...`).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Name of the RBAC group, 1 to 255 characters. Not unique: two groups can share a name.",
				Validators:          []validator.String{stringvalidator.UTF8LengthBetween(1, 255)},
			},
			"source_type": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "How the group was created: `direct` (in claude.ai or through the API, always the case for a group this resource creates) " +
					"or `scim` (provisioned by the identity provider). The API rejects renaming or deleting a `scim` group while SCIM provisioning is on.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"role_ids": schema.SetAttribute{
				Computed:    true,
				ElementType: types.StringType,
				MarkdownDescription: "IDs of the custom roles (`rbac_role_...`) attached to the group. Read-only: roles are attached in the claude.ai admin settings. " +
					"Null when the API reported role data as temporarily unavailable; the next refresh fills it in.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the group was created.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the group was last updated.",
			},
		},
	}
}

// --- Configure ---

func (r *RBACGroupResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// --- ModifyPlan ---

// ModifyPlan warns when a plan renames or destroys a group provisioned by the
// identity provider. The API rejects both with a 400 while SCIM provisioning is
// on, which the provider cannot know at plan time, so this is a warning rather
// than an error. A group this resource creates is always `direct`: a `scim`
// group only reaches state through an import.
func (r *RBACGroupResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() {
		return
	}

	var state rbacGroupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || state.SourceType.ValueString() != sourceTypeSCIM {
		return
	}

	if req.Plan.Raw.IsNull() {
		resp.Diagnostics.AddWarning(
			"Deleting an identity-provider group",
			fmt.Sprintf("RBAC group %s has source_type %q: the API rejects deleting it while SCIM provisioning is on. "+
				"Remove it from your identity provider instead, or use a `removed` block to stop managing it.", state.ID.ValueString(), sourceTypeSCIM),
		)
		return
	}

	var plan rbacGroupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !plan.Name.IsUnknown() && !plan.Name.Equal(state.Name) {
		resp.Diagnostics.AddAttributeWarning(
			path.Root("name"),
			"Renaming an identity-provider group",
			fmt.Sprintf("RBAC group %s has source_type %q: the API rejects renaming it while SCIM provisioning is on. "+
				"Rename it in your identity provider instead.", state.ID.ValueString(), sourceTypeSCIM),
		)
	}
}

// --- Create ---

func (r *RBACGroupResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan rbacGroupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body, err := r.client.DoRequest(ctx, "POST", rbacURL(rbacGroupsPath, nil), rbacGroupWriteRequest{Name: plan.Name.ValueString()})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create RBAC group: %s", err))
		return
	}

	r.setStateFromBody(ctx, body, "create", &resp.State, &resp.Diagnostics)
}

// --- Read ---

func (r *RBACGroupResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state rbacGroupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body, err := r.client.DoRequest(ctx, "GET", rbacURL(rbacGroupPath(state.ID.ValueString()), nil), nil)
	if err != nil {
		if admin.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read RBAC group: %s", err))
		return
	}

	r.setStateFromBody(ctx, body, "read", &resp.State, &resp.Diagnostics)
}

// --- Update ---

func (r *RBACGroupResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state rbacGroupModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body, err := r.client.DoRequest(ctx, "POST", rbacURL(rbacGroupPath(state.ID.ValueString()), nil), rbacGroupWriteRequest{Name: plan.Name.ValueString()})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update RBAC group: %s", err))
		return
	}

	r.setStateFromBody(ctx, body, "update", &resp.State, &resp.Diagnostics)
}

// --- Delete ---

// Delete removes the group. Its members stay in their organizations but lose
// the permissions of its roles. A 404 means it is already gone.
func (r *RBACGroupResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state rbacGroupModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.DoRequest(ctx, "DELETE", rbacURL(rbacGroupPath(state.ID.ValueString()), nil), nil)
	if err != nil && !admin.IsNotFound(err) {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete RBAC group: %s", err))
	}
}

// --- ImportState ---

func (r *RBACGroupResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// setStateFromBody decodes a group response and writes it to state.
func (r *RBACGroupResource) setStateFromBody(ctx context.Context, body []byte, op string, state *tfsdk.State, diags *diag.Diagnostics) {
	var group rbacGroupAPIResponse
	if err := json.Unmarshal(body, &group); err != nil {
		diags.AddError("Parse Error", fmt.Sprintf("Unable to parse %s RBAC group response: %s", op, err))
		return
	}
	model, d := mapRBACGroup(group)
	diags.Append(d...)
	if diags.HasError() {
		return
	}
	diags.Append(state.Set(ctx, &model)...)
}
