// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package apikeys

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
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

// APIKeyResourceModel is shared by the resource, the anthropic_api_key data
// source, and (through types.ObjectValueFrom) the anthropic_api_keys list
// items, whose attribute types are apiKeyListItemAttrTypes.
type APIKeyResourceModel struct {
	ID             types.String `tfsdk:"id"`
	Name           types.String `tfsdk:"name"`
	Status         types.String `tfsdk:"status"`
	WorkspaceID    types.String `tfsdk:"workspace_id"`
	Scope          types.Object `tfsdk:"scope"`
	Principal      types.Object `tfsdk:"principal"`
	ExpiresAt      types.String `tfsdk:"expires_at"`
	CreatedAt      types.String `tfsdk:"created_at"`
	CreatedBy      types.Object `tfsdk:"created_by"`
	PartialKeyHint types.String `tfsdk:"partial_key_hint"`
	Type           types.String `tfsdk:"type"`
}

var apiKeyCreatedByAttrTypes = map[string]attr.Type{
	"id":   types.StringType,
	"type": types.StringType,
}

var apiKeyScopeAttrTypes = map[string]attr.Type{
	"type":         types.StringType,
	"workspace_id": types.StringType,
}

var apiKeyPrincipalAttrTypes = map[string]attr.Type{
	"type":               types.StringType,
	"user_id":            types.StringType,
	"service_account_id": types.StringType,
}

// apiKeyUpdatableStatuses are the values POST /v1/organizations/api_keys/{id}
// accepts. The API also reports "expired", which it sets on its own once
// expires_at has passed and which cannot be written.
var apiKeyUpdatableStatuses = []string{"active", "archived", "inactive"}

// apiKeyStatuses are every status the API reports, and the values the list
// endpoint's status filter accepts.
var apiKeyStatuses = []string{"active", "archived", "expired", "inactive"}

// apiKeyWorkspaceIDDeprecation is shared by the resource and both data
// sources. internal/admin pins anthropic-version 2023-06-01, which still
// returns the field.
const apiKeyWorkspaceIDDeprecation = "Use scope.workspace_id instead. workspace_id is null both for " +
	"organization-scoped keys and for keys in the Default Workspace, and Admin API " +
	"version 2026-10-21 removes it."

const apiKeyWorkspaceIDDescription = "Use `scope.workspace_id` instead. ID of the workspace the key belongs to, " +
	"or null both for organization-scoped keys and for keys in the Default Workspace. " +
	"Admin API version 2026-10-21 removes this field."

const apiKeyScopeDescription = "Where the API key belongs: `type = \"workspace\"` with the workspace's real `workspace_id` " +
	"(the Default Workspace included), or `type = \"organization\"` (null `workspace_id`) for a principal-bound key that has no workspace."

const apiKeyPrincipalDescription = "The principal the API key acts as: `type = \"user_actor\"` with `user_id`, " +
	"or `type = \"service_account_actor\"` with `service_account_id`. " +
	"Null when the key is not bound to a principal (legacy workspace keys)."

const apiKeyExpiresAtDescription = "RFC 3339 timestamp at which the API key expires, or null if it never expires."

const apiKeyCreatedByDescription = "The actor that created this API key. `id` and `type` are null when the creator is not recorded " +
	"(legacy, workload-identity-federated or system-created keys)."

const apiKeyPartialKeyHintDescription = "Partially redacted hint of the API key, for identification. Null when the API returns none."

// --- Admin API request/response types ---

type apiKeyAPIResponse struct {
	ID          string  `json:"id"`
	Name        string  `json:"name"`
	Status      string  `json:"status"`
	WorkspaceID *string `json:"workspace_id"`
	// Scope is always present in the reference; the pointer keeps a payload
	// without it mapping to null rather than to an object of empty strings.
	Scope          *apiKeyScope     `json:"scope"`
	Principal      *apiKeyPrincipal `json:"principal"`
	ExpiresAt      string           `json:"expires_at"`
	CreatedAt      string           `json:"created_at"`
	CreatedBy      *apiKeyCreatedBy `json:"created_by"`
	PartialKeyHint string           `json:"partial_key_hint"`
	Type           string           `json:"type"`
}

type apiKeyCreatedBy struct {
	ID   string `json:"id"`
	Type string `json:"type"`
}

type apiKeyScope struct {
	Type        string `json:"type"`
	WorkspaceID string `json:"workspace_id"`
}

type apiKeyPrincipal struct {
	Type             string `json:"type"`
	UserID           string `json:"user_id"`
	ServiceAccountID string `json:"service_account_id"`
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
				MarkdownDescription: "Status of the API key. Can be set to `active`, `inactive` or `archived`; " +
					"setting `inactive` deactivates the key. " +
					"The API reports `expired` once `expires_at` has passed: that value is read-only and cannot be set. " +
					"Deleting this resource sets the status to `inactive` and removes it from state.",
				Validators:    []validator.String{stringvalidator.OneOf(apiKeyUpdatableStatuses...)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"workspace_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: apiKeyWorkspaceIDDescription,
				DeprecationMessage:  apiKeyWorkspaceIDDeprecation,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"scope": schema.SingleNestedAttribute{
				Computed:            true,
				MarkdownDescription: apiKeyScopeDescription,
				PlanModifiers:       []planmodifier.Object{stateUnlessPreUpgrade{}},
				Attributes: map[string]schema.Attribute{
					"type": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "Scope type: `workspace` or `organization`.",
					},
					"workspace_id": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "ID of the workspace the key belongs to, the Default Workspace included. Null when `type` is `organization`.",
					},
				},
			},
			"principal": schema.SingleNestedAttribute{
				Computed:            true,
				MarkdownDescription: apiKeyPrincipalDescription,
				PlanModifiers:       []planmodifier.Object{stateUnlessPreUpgrade{}},
				Attributes: map[string]schema.Attribute{
					"type": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "Principal type: `user_actor` or `service_account_actor`.",
					},
					"user_id": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "ID of the user the key acts as. Null unless `type` is `user_actor`.",
					},
					"service_account_id": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "ID of the service account the key acts as. Null unless `type` is `service_account_actor`.",
					},
				},
			},
			"expires_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: apiKeyExpiresAtDescription,
				PlanModifiers:       []planmodifier.String{stateUnlessPreUpgrade{}},
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the API key was created.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"created_by": schema.SingleNestedAttribute{
				Computed:            true,
				MarkdownDescription: apiKeyCreatedByDescription,
				PlanModifiers:       []planmodifier.Object{objectplanmodifier.UseStateForUnknown()},
				Attributes: map[string]schema.Attribute{
					"id": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "Identifier of the creator.",
					},
					"type": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "Type of the creator: `user` or `service_account`.",
					},
				},
			},
			"partial_key_hint": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: apiKeyPartialKeyHintDescription,
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

	planned := plan
	resp.Diagnostics.Append(mapAPIKeyToState(ctx, &key, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	keepLegacyEmptyValues(&plan, planned)

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
// Nullable fields (workspace_id, expires_at, principal, partial_key_hint,
// and the id and type of created_by) map to null rather than to empty
// strings.
func mapAPIKeyToState(_ context.Context, key *apiKeyAPIResponse, data *APIKeyResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics

	data.ID = types.StringValue(key.ID)
	data.Name = types.StringValue(key.Name)
	data.Status = types.StringValue(key.Status)
	data.CreatedAt = types.StringValue(key.CreatedAt)
	data.ExpiresAt = tfvalue.StringOrNull(key.ExpiresAt)
	data.PartialKeyHint = tfvalue.StringOrNull(key.PartialKeyHint)
	data.Type = types.StringValue(key.Type)

	if key.WorkspaceID != nil {
		data.WorkspaceID = types.StringValue(*key.WorkspaceID)
	} else {
		data.WorkspaceID = types.StringNull()
	}

	var d diag.Diagnostics
	data.CreatedBy, d = apiKeyCreatedByValue(key.CreatedBy)
	diags.Append(d...)
	data.Scope, d = apiKeyScopeValue(key.Scope)
	diags.Append(d...)
	data.Principal, d = apiKeyPrincipalValue(key.Principal)
	diags.Append(d...)

	return diags
}

// apiKeyCreatedByValue maps created_by. A key with no recorded creator (the
// API sends "created_by": null) still gets an object, with a null id and
// type: created_by has always been an object, holding empty strings before,
// and a configuration that reads created_by.id would fail on a null object
// ("Attempt to get attribute from null value").
func apiKeyCreatedByValue(c *apiKeyCreatedBy) (types.Object, diag.Diagnostics) {
	if c == nil {
		return types.ObjectValue(apiKeyCreatedByAttrTypes, map[string]attr.Value{
			"id":   types.StringNull(),
			"type": types.StringNull(),
		})
	}
	return types.ObjectValue(apiKeyCreatedByAttrTypes, map[string]attr.Value{
		"id":   types.StringValue(c.ID),
		"type": types.StringValue(c.Type),
	})
}

// keepLegacyEmptyValues keeps, in data, the empty created_by and
// partial_key_hint that planned holds where the API now sends null. State
// written before null mapping holds {id = "", type = ""} and "" for a key
// with no recorded creator or hint; UseStateForUnknown plans them as they
// are when the refresh is skipped (-refresh=false), and writing null back
// from Update would be an inconsistent result. The next refresh replaces
// them.
func keepLegacyEmptyValues(data *APIKeyResourceModel, planned APIKeyResourceModel) {
	if data.PartialKeyHint.IsNull() && planned.PartialKeyHint.Equal(types.StringValue("")) {
		data.PartialKeyHint = planned.PartialKeyHint
	}
	legacyCreatedBy := types.ObjectValueMust(apiKeyCreatedByAttrTypes, map[string]attr.Value{
		"id":   types.StringValue(""),
		"type": types.StringValue(""),
	})
	if noCreator, _ := apiKeyCreatedByValue(nil); data.CreatedBy.Equal(noCreator) && planned.CreatedBy.Equal(legacyCreatedBy) {
		data.CreatedBy = planned.CreatedBy
	}
}

// stateUnlessPreUpgrade is the plan modifier of scope, principal and
// expires_at. Like UseStateForUnknown it plans the prior state value, a real
// null included (a key that never expires, a legacy key with no principal),
// except over a state written before these attributes existed: they are all
// null there, and planning that null would make Update's values an
// inconsistent result when the refresh is skipped (-refresh=false). Such a
// state is told apart by a null scope, which the API always sends; the value
// is then left unknown for Update to fill.
type stateUnlessPreUpgrade struct{}

func (stateUnlessPreUpgrade) Description(context.Context) string {
	return "Keeps the prior state value, unless the state predates the attribute."
}

func (m stateUnlessPreUpgrade) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

// keep reports whether the prior state value should be planned.
func (stateUnlessPreUpgrade) keep(ctx context.Context, state tfsdk.State, planUnknown, configUnknown bool, diags *diag.Diagnostics) bool {
	if state.Raw.IsNull() || !planUnknown || configUnknown {
		return false
	}
	var scope types.Object
	diags.Append(state.GetAttribute(ctx, path.Root("scope"), &scope)...)
	return !diags.HasError() && !scope.IsNull()
}

func (m stateUnlessPreUpgrade) PlanModifyObject(ctx context.Context, req planmodifier.ObjectRequest, resp *planmodifier.ObjectResponse) {
	if m.keep(ctx, req.State, req.PlanValue.IsUnknown(), req.ConfigValue.IsUnknown(), &resp.Diagnostics) {
		resp.PlanValue = req.StateValue
	}
}

func (m stateUnlessPreUpgrade) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if m.keep(ctx, req.State, req.PlanValue.IsUnknown(), req.ConfigValue.IsUnknown(), &resp.Diagnostics) {
		resp.PlanValue = req.StateValue
	}
}

func apiKeyScopeValue(s *apiKeyScope) (types.Object, diag.Diagnostics) {
	if s == nil {
		return types.ObjectNull(apiKeyScopeAttrTypes), nil
	}
	return types.ObjectValue(apiKeyScopeAttrTypes, map[string]attr.Value{
		"type":         types.StringValue(s.Type),
		"workspace_id": tfvalue.StringOrNull(s.WorkspaceID),
	})
}

func apiKeyPrincipalValue(p *apiKeyPrincipal) (types.Object, diag.Diagnostics) {
	if p == nil {
		return types.ObjectNull(apiKeyPrincipalAttrTypes), nil
	}
	return types.ObjectValue(apiKeyPrincipalAttrTypes, map[string]attr.Value{
		"type":               types.StringValue(p.Type),
		"user_id":            tfvalue.StringOrNull(p.UserID),
		"service_account_id": tfvalue.StringOrNull(p.ServiceAccountID),
	})
}
