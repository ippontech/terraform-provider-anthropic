// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

// Package compliance implements the organization's Compliance API settings:
// the anthropic_compliance_settings singleton resource, through the Admin API
// client (internal/admin).
package compliance

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
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &ComplianceSettingsResource{}
var _ resource.ResourceWithImportState = &ComplianceSettingsResource{}

const (
	// complianceSettingsPath addresses the per-organization singleton; the GA
	// reference documents it without an identifier and without `?beta=true`.
	complianceSettingsPath = "/v1/organizations/compliance_settings"
	organizationPath       = "/v1/organizations/me"
)

func NewComplianceSettingsResource() resource.Resource {
	return &ComplianceSettingsResource{}
}

// ComplianceSettingsResource defines the resource implementation.
type ComplianceSettingsResource struct {
	client *admin.Client
}

// ComplianceSettingsResourceModel maps the resource schema to Go types.
type ComplianceSettingsResourceModel struct {
	ID    types.String `tfsdk:"id"`
	State types.String `tfsdk:"state"`
}

// --- Admin API request/response types ---

type complianceSettingsAPIResponse struct {
	Type string `json:"type"`
	// State is documented as the object form {"type": "enabled"}; the string
	// shorthand the update request accepts is decoded too (see parseState).
	State json.RawMessage `json:"state"`
}

type complianceSettingsState struct {
	Type string `json:"type"`
}

type complianceSettingsUpdateRequest struct {
	State complianceSettingsState `json:"state"`
}

type organizationAPIResponse struct {
	ID string `json:"id"`
}

// --- Schema ---

func (r *ComplianceSettingsResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_compliance_settings"
}

func (r *ComplianceSettingsResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Turns the organization's Compliance API on or off via the Admin API. " +
			"The settings are an organization-wide singleton: creating the resource adopts them, and destroying it " +
			"only removes it from the Terraform state and leaves the Compliance API as it is. " +
			"Requires `admin_api_key` (or `ANTHROPIC_ADMIN_API_KEY`) to be configured on the provider.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "ID of the organization the settings belong to: the organization of the configured admin key.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"state": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "`enabled` turns on the Compliance API and starts recording organization activity events; " +
					"`disabled` turns both off. Activity that happens while it is disabled is never recorded. " +
					"An Access Transparency provisioning re-enables it after a `disabled`, which the next plan reports as drift.",
				Validators: []validator.String{stringvalidator.OneOf("enabled", "disabled")},
			},
		},
	}
}

// --- Configure ---

func (r *ComplianceSettingsResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// Create adopts the singleton: it resolves the organization ID for `id`, then
// applies the planned state. The update is idempotent, so adopting settings
// already in the planned state changes nothing.
func (r *ComplianceSettingsResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ComplianceSettingsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	orgID, diags := r.organizationID(ctx)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	state, diags := r.update(ctx, plan.State.ValueString())
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	// On a mismatch nothing is stored, as for an API error: the next apply
	// tries again and reports it again.
	resp.Diagnostics.Append(stateNotApplied(plan.State.ValueString(), state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &ComplianceSettingsResourceModel{
		ID:    types.StringValue(orgID),
		State: types.StringValue(state),
	})...)
}

// --- Read ---

// Read never removes the resource: the singleton always exists, so any error
// (a 404 included) is reported instead of planning a re-creation.
func (r *ComplianceSettingsResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data ComplianceSettingsResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(r.checkOrganization(ctx, data.ID)...)
	if resp.Diagnostics.HasError() {
		return
	}

	respBytes, err := r.client.DoRequest(ctx, "GET", complianceSettingsPath, nil)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read compliance settings: %s", err))
		return
	}

	state, diags := parseComplianceSettings(respBytes, "read")
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	data.State = types.StringValue(state)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Update ---

func (r *ComplianceSettingsResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, prior ComplianceSettingsResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &prior)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Checked again here, not only in Read, for an apply with -refresh=false.
	resp.Diagnostics.Append(r.checkOrganization(ctx, prior.ID)...)
	if resp.Diagnostics.HasError() {
		return
	}

	state, diags := r.update(ctx, plan.State.ValueString())
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	// On a mismatch the planned value is kept along with the error, so the
	// error says why instead of Terraform's inconsistent-result message; the
	// next refresh reads the actual state.
	resp.Diagnostics.Append(stateNotApplied(plan.State.ValueString(), state)...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// --- Delete ---

// Delete only forgets the settings. Resetting them to `disabled` was the
// alternative and is deliberately not done: turning the Compliance API off
// stops activity recording (and Access Transparency event delivery), and the
// activity missed meanwhile can never be recovered, so a destroy, a `removed`
// block or a refactor that drops the resource must not do it implicitly. An
// organization that wants it off sets `state = "disabled"` and applies first;
// for one enrolled in Access Transparency the reset would not even last, since
// provisioning re-enables it.
func (r *ComplianceSettingsResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data ComplianceSettingsResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.AddWarning("Compliance settings left unchanged",
		fmt.Sprintf("anthropic_compliance_settings was removed from the Terraform state only: the Compliance API of the organization stays %s. "+
			"To turn it off, set state = \"disabled\" and apply before removing the resource.", data.State.ValueString()))
}

// --- ImportState ---

// ImportState accepts any ID, since the settings are addressed through the
// admin key's organization rather than by ID; by convention, pass the
// organization ID, and any other ID gets a warning naming the organization
// imported. The state's `id` is always the organization ID resolved from the
// API, exactly as Create sets it.
func (r *ComplianceSettingsResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	orgID, diags := r.organizationID(ctx)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	if req.ID != orgID {
		resp.Diagnostics.AddWarning("Imported the admin key's organization",
			fmt.Sprintf("The import ID %q is not the ID of the admin key's organization: the compliance settings of organization %s were imported. "+
				"Check that the admin key belongs to the organization you meant to manage.", req.ID, orgID))
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), orgID)...)
}

// ============================================================================
// Helper functions
// ============================================================================

// organizationID returns the ID of the organization the admin key belongs to.
func (r *ComplianceSettingsResource) organizationID(ctx context.Context) (string, diag.Diagnostics) {
	var diags diag.Diagnostics
	respBytes, err := r.client.DoRequest(ctx, "GET", organizationPath, nil)
	if err != nil {
		diags.AddError("Client Error", fmt.Sprintf("Unable to read the organization of the admin key: %s", err))
		return "", diags
	}

	var org organizationAPIResponse
	if err := json.Unmarshal(respBytes, &org); err != nil {
		diags.AddError("Parse Error", fmt.Sprintf("Unable to parse organization response: %s", err))
		return "", diags
	}
	if org.ID == "" {
		diags.AddError("Parse Error", "The organization response carries no ID.")
		return "", diags
	}
	return org.ID, diags
}

// checkOrganization fails when the admin key belongs to another organization
// than the one in state. The settings are addressed through the key alone, so
// no 404 would reveal a key from another organization, and an apply would
// switch that organization's Compliance API under the stored ID.
func (r *ComplianceSettingsResource) checkOrganization(ctx context.Context, stateID types.String) diag.Diagnostics {
	if stateID.IsNull() || stateID.IsUnknown() || stateID.ValueString() == "" {
		return nil
	}
	orgID, diags := r.organizationID(ctx)
	if diags.HasError() || orgID == stateID.ValueString() {
		return diags
	}
	diags.AddError("Admin Key Of Another Organization",
		fmt.Sprintf("The configured admin key belongs to organization %s, but this anthropic_compliance_settings manages organization %s. "+
			"The settings are addressed through the admin key alone, so an apply would change the Compliance API of %s. "+
			"Configure an admin key of organization %s, or remove the resource from the state and import it again to manage %s.",
			orgID, stateID.ValueString(), orgID, stateID.ValueString(), orgID))
	return diags
}

// stateNotApplied reports a POST that answered with another state than the
// one sent.
func stateNotApplied(planned, got string) diag.Diagnostics {
	var diags diag.Diagnostics
	if got != planned {
		diags.AddError("Compliance Settings Not Applied",
			fmt.Sprintf("The API answered state %q after the provider set %q. "+
				"An organization linked to a parent organization reads the parent's setting, which the parent's primary owner manages in claude.ai.",
				got, planned))
	}
	return diags
}

// update sends the desired state in its canonical object form and returns the
// state the API answers with.
func (r *ComplianceSettingsResource) update(ctx context.Context, state string) (string, diag.Diagnostics) {
	body := complianceSettingsUpdateRequest{State: complianceSettingsState{Type: state}}
	respBytes, err := r.client.DoRequest(ctx, "POST", complianceSettingsPath, body)
	if err != nil {
		var diags diag.Diagnostics
		diags.AddError("Client Error", fmt.Sprintf("Unable to update compliance settings: %s", err))
		return "", diags
	}
	return parseComplianceSettings(respBytes, "update")
}

// parseComplianceSettings returns state.type from a compliance settings payload.
func parseComplianceSettings(respBytes []byte, op string) (string, diag.Diagnostics) {
	var diags diag.Diagnostics
	var settings complianceSettingsAPIResponse
	if err := json.Unmarshal(respBytes, &settings); err != nil {
		diags.AddError("Parse Error", fmt.Sprintf("Unable to parse %s compliance settings response: %s", op, err))
		return "", diags
	}
	state, err := parseState(settings.State)
	if err != nil {
		diags.AddError("Parse Error", fmt.Sprintf("Unable to parse %s compliance settings state: %s", op, err))
		return "", diags
	}
	return state, diags
}

// parseState decodes `state` in either form the API documents: the canonical
// object {"type": "enabled"} or the string shorthand "enabled".
func parseState(raw json.RawMessage) (string, error) {
	var short string
	if json.Unmarshal(raw, &short) == nil && short != "" {
		return short, nil
	}
	var obj complianceSettingsState
	if err := json.Unmarshal(raw, &obj); err != nil {
		return "", err
	}
	if obj.Type == "" {
		return "", fmt.Errorf("missing state.type in %s", string(raw))
	}
	return obj.Type, nil
}
