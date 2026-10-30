// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package organizationwebhooks

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
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
var _ resource.Resource = &OrganizationWebhookResource{}
var _ resource.ResourceWithImportState = &OrganizationWebhookResource{}

func NewOrganizationWebhookResource() resource.Resource {
	return &OrganizationWebhookResource{}
}

// OrganizationWebhookResource manages an organization webhook endpoint.
type OrganizationWebhookResource struct {
	client *admin.Client
}

// OrganizationWebhookResourceModel is OrganizationWebhookModel plus the
// signing secret and its rotation trigger, which only the resource has.
type OrganizationWebhookResourceModel struct {
	ID                   types.String `tfsdk:"id"`
	URL                  types.String `tfsdk:"url"`
	EnabledEvents        types.Set    `tfsdk:"enabled_events"`
	Name                 types.String `tfsdk:"name"`
	Description          types.String `tfsdk:"description"`
	Enabled              types.Bool   `tfsdk:"enabled"`
	DisabledReason       types.String `tfsdk:"disabled_reason"`
	SigningSecret        types.String `tfsdk:"signing_secret"`
	SigningSecretVersion types.Int64  `tfsdk:"signing_secret_version"`
	CreatedAt            types.String `tfsdk:"created_at"`
	UpdatedAt            types.String `tfsdk:"updated_at"`
}

type organizationWebhookCreateRequest struct {
	URL           string   `json:"url"`
	EnabledEvents []string `json:"enabled_events"`
	Name          string   `json:"name,omitempty"`
	Description   string   `json:"description,omitempty"`
}

// organizationWebhookUpdateRequest carries only the fields that change: the
// API leaves every omitted field as it is.
type organizationWebhookUpdateRequest struct {
	URL           *string  `json:"url,omitempty"`
	EnabledEvents []string `json:"enabled_events,omitempty"`
	Name          *string  `json:"name,omitempty"`
	Description   *string  `json:"description,omitempty"`
	State         string   `json:"state,omitempty"`
}

func (u organizationWebhookUpdateRequest) isEmpty() bool {
	return u.URL == nil && u.EnabledEvents == nil && u.Name == nil && u.Description == nil && u.State == ""
}

// --- Schema ---

func (r *OrganizationWebhookResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_organization_webhook"
}

func (r *OrganizationWebhookResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages an organization webhook endpoint via the Admin API (beta, `webhooks-2026-10-07`): an HTTPS URL that " +
			"receives events about enforcement actions on the organization's user profiles. The signing secret is returned " +
			"only when the endpoint is created or the secret regenerated, so it is stored in Terraform state (sensitive). " +
			"Requires `admin_api_key` (or `ANTHROPIC_ADMIN_API_KEY`) to be configured on the provider.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "ID of the webhook endpoint (`wep_...`).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"url": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "The HTTPS URL that deliveries are sent to: port 443, a public domain name rather than an IP address. " +
					"Two enabled organization webhook endpoints cannot share a URL.",
				Validators: []validator.String{
					stringvalidator.RegexMatches(regexp.MustCompile(`^https://`), "must be an HTTPS URL"),
				},
			},
			"enabled_events": schema.SetAttribute{
				Required:    true,
				ElementType: types.StringType,
				MarkdownDescription: "The event types delivered to the endpoint, at least one of " +
					organizationWebhookEventTypesMarkdown() + ". An update replaces the endpoint's whole set.",
				Validators: []validator.Set{
					setvalidator.SizeAtLeast(1),
					setvalidator.ValueStringsAre(stringvalidator.OneOf(organizationWebhookEventTypes...)),
				},
			},
			"name": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "A short label for the endpoint. Removing it from the configuration keeps the current value.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"description": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Notes about the endpoint. Removing it from the configuration keeps the current value.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
				Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
			},
			"enabled": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(true),
				MarkdownDescription: "Whether deliveries are sent to the endpoint (the API's `state`). Default: `true`. " +
					"Anthropic can disable an endpoint on its own (see `disabled_reason`); the next apply then turns it back on. " +
					"Events that happen while the endpoint is disabled are never delivered.",
			},
			"disabled_reason": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Why Anthropic disabled the endpoint automatically, or null when it is enabled or was disabled through `enabled = false`.",
			},
			"signing_secret": schema.StringAttribute{
				Computed:  true,
				Sensitive: true,
				MarkdownDescription: "The `whsec_`-prefixed secret Anthropic signs deliveries with ([Standard Webhooks](https://www.standardwebhooks.com/)). " +
					"Only the create and regenerate responses carry it, so the provider keeps it in state from those calls. " +
					"Null after `terraform import`: change `signing_secret_version` to regenerate it and capture the new value.",
				PlanModifiers: []planmodifier.String{signingSecretPlanModifier{}},
			},
			"signing_secret_version": schema.Int64Attribute{
				Optional: true,
				MarkdownDescription: "Rotation trigger for `signing_secret`. Changing it to a new value (setting it for the first time on an existing endpoint included) " +
					"regenerates the secret on the next apply: the old secret stops working at once, so update the receiver right away.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the endpoint was created.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of the last change to the endpoint.",
			},
		},
	}
}

// organizationWebhookEventTypesMarkdown renders the accepted event types for
// the schema documentation.
func organizationWebhookEventTypesMarkdown() string {
	quoted := make([]string, len(organizationWebhookEventTypes))
	for i, e := range organizationWebhookEventTypes {
		quoted[i] = "`" + e + "`"
	}
	return strings.Join(quoted, ", ")
}

// --- Configure ---

func (r *OrganizationWebhookResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *OrganizationWebhookResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data OrganizationWebhookResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body := organizationWebhookCreateRequest{URL: data.URL.ValueString()}
	resp.Diagnostics.Append(data.EnabledEvents.ElementsAs(ctx, &body.EnabledEvents, false)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if isKnown(data.Name) {
		body.Name = data.Name.ValueString()
	}
	if isKnown(data.Description) {
		body.Description = data.Description.ValueString()
	}

	respBytes, err := doOrganizationWebhookRequest(ctx, r.client, "POST", organizationWebhooksPath, body)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create organization webhook: %s", err))
		return
	}

	var created organizationWebhookAPIResponse
	if err := json.Unmarshal(respBytes, &created); err != nil {
		resp.Diagnostics.AddError("Parse Error", fmt.Sprintf("Unable to parse create organization webhook response: %s", err))
		return
	}

	// The create response is the only one, with regenerate, that carries the
	// signing secret: keep it before anything else can fail.
	wantEnabled := data.Enabled
	data.SigningSecret = tfvalue.StringOrNull(created.SigningSecret)
	resp.Diagnostics.Append(applyOrganizationWebhookResponse(&created, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// The create body has no state, so enabled = false takes a second call. A
	// new endpoint should start enabled; if the API created it disabled (for
	// example over its URL), the same call turns it on as planned.
	if wantEnabled.IsNull() || wantEnabled.IsUnknown() || data.Enabled.Equal(wantEnabled) {
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		return
	}
	want := organizationWebhookStateDisabled
	if wantEnabled.ValueBool() {
		want = organizationWebhookStateEnabled
	}
	updated, err := r.update(ctx, created.ID, organizationWebhookUpdateRequest{State: want})
	if err == nil {
		resp.Diagnostics.Append(applyOrganizationWebhookResponse(updated, &data)...)
		if !resp.Diagnostics.HasError() && !data.Enabled.Equal(wantEnabled) {
			err = fmt.Errorf("the API still reports it %s (disabled_reason: %q)", updated.State, updated.DisabledReason)
		}
	}
	// Save the endpoint as it is (with its secret) so it is not orphaned;
	// on an error Terraform marks it tainted.
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Organization webhook %s was created but could not be set to state %q: %s", created.ID, want, err))
	}
}

// --- Read ---

func (r *OrganizationWebhookResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data OrganizationWebhookResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	ep, err := r.get(ctx, data.ID.ValueString())
	if err != nil {
		if !admin.IsNotFound(err) {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read organization webhook: %s", err))
			return
		}
		if lerr := organizationWebhooksUnavailable(ctx, r.client); lerr != nil {
			resp.Diagnostics.AddError("Organization webhooks unavailable", fmt.Sprintf(
				"Organization webhook %s answered 404, and so did the list of endpoints (%s). The organization may have lost the "+
					"webhooks beta, or the %q beta version may be retired: the endpoint is kept in state, with its signing secret, "+
					"rather than dropped as deleted.", data.ID.ValueString(), lerr, organizationWebhooksBeta))
			return
		}
		resp.State.RemoveResource(ctx)
		return
	}

	// No read returns the signing secret: signing_secret and
	// signing_secret_version keep their prior state values (null after an
	// import), since applyOrganizationWebhookResponse leaves them alone.
	resp.Diagnostics.Append(applyOrganizationWebhookResponse(ep, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Update ---

func (r *OrganizationWebhookResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state OrganizationWebhookResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.ID.ValueString()

	// On an error Terraform saves the state the response holds. The framework
	// starts it as the prior state, so every error path below that sets
	// nothing keeps the old signing secret and version.

	body, diags := buildOrganizationWebhookUpdate(ctx, plan, state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	var ep *organizationWebhookAPIResponse
	if !body.isEmpty() {
		updated, err := r.update(ctx, id, body)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update organization webhook: %s", err))
			return
		}
		ep = updated
	}

	// Without a regeneration the secret stays the one in state.
	plan.SigningSecret = state.SigningSecret
	if signingSecretRegenerationRequested(plan.SigningSecretVersion, state.SigningSecretVersion) {
		secret, err := r.regenerateSigningSecret(ctx, id)
		if err != nil {
			if ep != nil {
				// The update landed: record it, but keep the old secret and the
				// old version so the next plan asks for the regeneration again.
				plan.SigningSecretVersion = state.SigningSecretVersion
				resp.Diagnostics.Append(applyOrganizationWebhookResponse(ep, &plan)...)
				resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
			}
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to regenerate the organization webhook signing secret: %s", err))
			return
		}
		plan.SigningSecret = tfvalue.StringOrNull(secret)
	}

	if ep == nil {
		got, err := r.get(ctx, id)
		if err != nil {
			if plan.SigningSecret.Equal(state.SigningSecret) {
				resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read organization webhook: %s", err))
				return
			}
			// The secret was regenerated and exists nowhere else: save it with
			// the computed values of the prior state rather than lose it.
			plan.UpdatedAt = state.UpdatedAt
			plan.DisabledReason = state.DisabledReason
			resp.Diagnostics.AddWarning("Organization webhook not refreshed",
				fmt.Sprintf("The signing secret was regenerated and saved, but reading the endpoint back failed: %s. The next refresh corrects the other attributes.", err))
			resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
			return
		}
		ep = got
	}

	resp.Diagnostics.Append(applyOrganizationWebhookResponse(ep, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// --- Delete ---

func (r *OrganizationWebhookResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data OrganizationWebhookResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := doOrganizationWebhookRequest(ctx, r.client, "DELETE", organizationWebhookPath(data.ID.ValueString()), nil)
	switch {
	case err == nil:
	case !admin.IsNotFound(err):
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete organization webhook: %s", err))
	default:
		// A 404 means already deleted only if the API answers at all.
		if lerr := organizationWebhooksUnavailable(ctx, r.client); lerr != nil {
			resp.Diagnostics.AddError("Organization webhooks unavailable", fmt.Sprintf(
				"Deleting organization webhook %s answered 404, and so did the list of endpoints (%s): the organization may have lost "+
					"the webhooks beta, so the endpoint may still exist and receive events.", data.ID.ValueString(), lerr))
		}
	}
}

// --- ImportState ---

// ImportState imports an endpoint by ID. The API never returns the signing
// secret on a read, so it stays null until signing_secret_version changes.
func (r *OrganizationWebhookResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// ============================================================================
// API calls
// ============================================================================

func (r *OrganizationWebhookResource) get(ctx context.Context, id string) (*organizationWebhookAPIResponse, error) {
	respBytes, err := doOrganizationWebhookRequest(ctx, r.client, "GET", organizationWebhookPath(id), nil)
	if err != nil {
		return nil, err
	}
	var ep organizationWebhookAPIResponse
	if err := json.Unmarshal(respBytes, &ep); err != nil {
		return nil, fmt.Errorf("parse organization webhook response: %w", err)
	}
	return &ep, nil
}

func (r *OrganizationWebhookResource) update(ctx context.Context, id string, body organizationWebhookUpdateRequest) (*organizationWebhookAPIResponse, error) {
	respBytes, err := doOrganizationWebhookRequest(ctx, r.client, "POST", organizationWebhookPath(id), body)
	if err != nil {
		return nil, err
	}
	var ep organizationWebhookAPIResponse
	if err := json.Unmarshal(respBytes, &ep); err != nil {
		return nil, fmt.Errorf("parse organization webhook update response: %w", err)
	}
	return &ep, nil
}

func (r *OrganizationWebhookResource) regenerateSigningSecret(ctx context.Context, id string) (string, error) {
	respBytes, err := doOrganizationWebhookRequest(ctx, r.client, "POST", organizationWebhookPath(id)+"/regenerate_signing_secret", nil)
	if err != nil {
		return "", err
	}
	var out organizationWebhookSigningSecretResponse
	if err := json.Unmarshal(respBytes, &out); err != nil {
		return "", fmt.Errorf("parse regenerate signing secret response: %w", err)
	}
	return out.SigningSecret, nil
}

// ============================================================================
// Helper functions
// ============================================================================

// applyOrganizationWebhookResponse copies an API endpoint into the resource
// model. It never touches signing_secret or signing_secret_version, which no
// read returns.
func applyOrganizationWebhookResponse(ep *organizationWebhookAPIResponse, data *OrganizationWebhookResourceModel) diag.Diagnostics {
	m, diags := mapOrganizationWebhookToModel(ep)
	data.ID = m.ID
	data.URL = m.URL
	data.EnabledEvents = m.EnabledEvents
	data.Name = m.Name
	data.Description = m.Description
	data.Enabled = m.Enabled
	data.DisabledReason = m.DisabledReason
	data.CreatedAt = m.CreatedAt
	data.UpdatedAt = m.UpdatedAt
	return diags
}

// buildOrganizationWebhookUpdate returns the update body: only the fields
// whose planned value differs from state. name and description are Optional
// + Computed, so a null plan (attribute removed) keeps the stored value.
func buildOrganizationWebhookUpdate(ctx context.Context, plan, state OrganizationWebhookResourceModel) (organizationWebhookUpdateRequest, diag.Diagnostics) {
	var body organizationWebhookUpdateRequest
	var diags diag.Diagnostics

	if isKnown(plan.URL) && !plan.URL.Equal(state.URL) {
		v := plan.URL.ValueString()
		body.URL = &v
	}
	if isKnown(plan.Name) && !plan.Name.Equal(state.Name) {
		v := plan.Name.ValueString()
		body.Name = &v
	}
	if isKnown(plan.Description) && !plan.Description.Equal(state.Description) {
		v := plan.Description.ValueString()
		body.Description = &v
	}
	if !plan.EnabledEvents.IsNull() && !plan.EnabledEvents.IsUnknown() && !plan.EnabledEvents.Equal(state.EnabledEvents) {
		var events []string
		diags.Append(plan.EnabledEvents.ElementsAs(ctx, &events, false)...)
		body.EnabledEvents = events
	}
	if !plan.Enabled.IsNull() && !plan.Enabled.IsUnknown() && !plan.Enabled.Equal(state.Enabled) {
		body.State = organizationWebhookStateDisabled
		if plan.Enabled.ValueBool() {
			body.State = organizationWebhookStateEnabled
		}
	}
	return body, diags
}

func isKnown(v types.String) bool {
	return !v.IsNull() && !v.IsUnknown()
}

// signingSecretRegenerationRequested reports whether Update must regenerate
// the signing secret: signing_secret_version is set to a value other than the
// one in state. Removing it (a null plan) is not a request.
func signingSecretRegenerationRequested(plan, state types.Int64) bool {
	return !plan.IsNull() && !plan.IsUnknown() && !plan.Equal(state)
}

// signingSecretPlanModifier plans the signing secret from state, null after an
// import included, unless signing_secret_version changes: Update then
// regenerates the secret and the new value is known only after apply.
type signingSecretPlanModifier struct{}

func (signingSecretPlanModifier) Description(context.Context) string {
	return "Keeps the signing secret from state unless signing_secret_version changes."
}

func (m signingSecretPlanModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (signingSecretPlanModifier) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	// Create (no state: the secret comes from the create response) or destroy.
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}

	var planVersion, stateVersion types.Int64
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("signing_secret_version"), &planVersion)...)
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("signing_secret_version"), &stateVersion)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if planVersion.IsUnknown() || signingSecretRegenerationRequested(planVersion, stateVersion) {
		resp.PlanValue = types.StringUnknown()
		return
	}
	resp.PlanValue = req.StateValue
}
