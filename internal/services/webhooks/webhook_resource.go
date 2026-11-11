// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package webhooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
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
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &WebhookResource{}
var _ resource.ResourceWithImportState = &WebhookResource{}

func NewWebhookResource() resource.Resource {
	return &WebhookResource{}
}

// WebhookResource manages an webhook endpoint.
type WebhookResource struct {
	client *anthropic.Client
}

// WebhookResourceModel is WebhookModel plus the
// signing secret and its rotation trigger, which only the resource has.
type WebhookResourceModel struct {
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

type webhookCreateRequest struct {
	URL           string   `json:"url"`
	EnabledEvents []string `json:"enabled_events"`
	Name          string   `json:"name,omitempty"`
	Description   string   `json:"description,omitempty"`
}

// webhookUpdateRequest carries only the fields that change: the
// API leaves every omitted field as it is.
type webhookUpdateRequest struct {
	URL           *string  `json:"url,omitempty"`
	EnabledEvents []string `json:"enabled_events,omitempty"`
	Name          *string  `json:"name,omitempty"`
	Description   *string  `json:"description,omitempty"`
	State         string   `json:"state,omitempty"`
}

func (u webhookUpdateRequest) isEmpty() bool {
	return u.URL == nil && u.EnabledEvents == nil && u.Name == nil && u.Description == nil && u.State == ""
}

// --- Schema ---

func (r *WebhookResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_webhook"
}

func (r *WebhookResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "**Experimental.** Manages a workspace webhook endpoint for Managed Agents events (beta, " +
			"`managed-agents-2026-04-01`): an HTTPS URL that receives session, vault, agent, deployment, environment and " +
			"memory store events. Anthropic does not document this API in its API reference yet, so this resource may " +
			"change or be removed in a minor release. The signing secret is returned only when the endpoint is created or " +
			"the secret regenerated, so it is stored in Terraform state (sensitive). Uses the standard `api_key` (or " +
			"`ANTHROPIC_API_KEY`), and the endpoint belongs to that key's workspace.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "ID of the webhook endpoint (`wep_...`).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"url": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The HTTPS URL that deliveries are sent to: port 443, with a publicly resolvable hostname.",
				Validators: []validator.String{
					stringvalidator.RegexMatches(regexp.MustCompile(`^https://`), "must be an HTTPS URL"),
				},
			},
			"enabled_events": schema.SetAttribute{
				Required:    true,
				ElementType: types.StringType,
				MarkdownDescription: "The event types delivered to the endpoint, at least one of " +
					webhookEventTypesMarkdown() + ". An update replaces the endpoint's whole set.",
				Validators: []validator.Set{
					setvalidator.SizeAtLeast(1),
					setvalidator.ValueStringsAre(stringvalidator.OneOf(webhookEventTypes...)),
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

// webhookEventTypesMarkdown renders the accepted event types for
// the schema documentation.
func webhookEventTypesMarkdown() string {
	quoted := make([]string, len(webhookEventTypes))
	for i, e := range webhookEventTypes {
		quoted[i] = "`" + e + "`"
	}
	return strings.Join(quoted, ", ")
}

// --- Configure ---

func (r *WebhookResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

	if !providerrors.RequireResourceAPIClient(pd.Client, &resp.Diagnostics) {
		return
	}

	r.client = pd.Client
}

// --- Create ---

func (r *WebhookResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data WebhookResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body := webhookCreateRequest{URL: data.URL.ValueString()}
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

	respBytes, err := doWebhookRequest(ctx, r.client, "POST", webhooksPath, body)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create webhook: %s", err))
		return
	}

	var created webhookAPIResponse
	if err := json.Unmarshal(respBytes, &created); err != nil {
		resp.Diagnostics.AddError("Parse Error", fmt.Sprintf("Unable to parse create webhook response: %s", err))
		return
	}

	// The create response is the only one, with regenerate, that carries the
	// signing secret: keep it before anything else can fail.
	wantEnabled := data.Enabled
	data.SigningSecret = tfvalue.StringOrNull(created.SigningSecret)
	resp.Diagnostics.Append(applyWebhookResponse(&created, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// The create body has no state, so enabled = false takes a second call. A
	// new endpoint should start enabled; if the API created it disabled (for
	// example over its URL), the same call turns it on as planned.
	if wantEnabled.IsNull() || wantEnabled.IsUnknown() || data.Enabled.Equal(wantEnabled) {
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		addMissingSigningSecretError(&resp.Diagnostics, created)
		return
	}
	want := webhookStateDisabled
	if wantEnabled.ValueBool() {
		want = webhookStateEnabled
	}
	updated, err := r.update(ctx, created.ID, webhookUpdateRequest{State: want})
	if err == nil {
		resp.Diagnostics.Append(applyWebhookResponse(updated, &data)...)
		if !resp.Diagnostics.HasError() && !data.Enabled.Equal(wantEnabled) {
			err = fmt.Errorf("the API still reports it %s (disabled_reason: %q)", updated.State, updated.DisabledReason)
		}
	}
	// Save the endpoint as it is (with its secret) so it is not orphaned;
	// on an error Terraform marks it tainted.
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Webhook %s was created but could not be set to state %q: %s", created.ID, want, err))
	}
	addMissingSigningSecretError(&resp.Diagnostics, created)
}

// addMissingSigningSecretError fails a create whose 2xx response carries no
// signing_secret. The endpoint is already saved to state (Terraform marks it
// tainted), so it is not orphaned, but an endpoint without a known secret is
// unusable, and the plan never offered it as null.
func addMissingSigningSecretError(diags *diag.Diagnostics, created webhookAPIResponse) {
	if created.SigningSecret != "" {
		return
	}
	diags.AddError("Missing Signing Secret",
		fmt.Sprintf("Webhook %s was created, but the API response carries no signing_secret. "+
			"The endpoint is saved and marked tainted; the next apply replaces it.", created.ID))
}

// --- Read ---

func (r *WebhookResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data WebhookResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	ep, err := r.get(ctx, data.ID.ValueString())
	if err != nil {
		if !isNotFound(err) {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read webhook: %s", err))
			return
		}
		if lerr := webhooksUnavailable(ctx, r.client); lerr != nil {
			resp.Diagnostics.AddError("Webhooks unavailable", fmt.Sprintf(
				"Webhook %s answered 404, and so did the list of endpoints (%s). The %q beta version may be retired, "+
					"or the provider may point at another workspace: the endpoint is kept in state, with its signing secret, "+
					"rather than dropped as deleted.", data.ID.ValueString(), lerr, webhooksBeta))
			return
		}
		resp.State.RemoveResource(ctx)
		return
	}

	// No read returns the signing secret: signing_secret and
	// signing_secret_version keep their prior state values (null after an
	// import), since applyWebhookResponse leaves them alone.
	resp.Diagnostics.Append(applyWebhookResponse(ep, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Update ---

func (r *WebhookResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state WebhookResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.ID.ValueString()

	// On an error Terraform saves the state the response holds. Framework
	// v1.19.0 already starts it as the prior state, but its doc comment says the
	// plan, where the secret is unknown: set it explicitly, so every error path
	// below that sets nothing keeps the old signing secret and version.
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body, diags := buildWebhookUpdate(ctx, plan, state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	var ep *webhookAPIResponse
	if !body.isEmpty() {
		updated, err := r.update(ctx, id, body)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update webhook: %s", err))
			return
		}
		ep = updated
	}

	// Without a regeneration the secret stays the one in state.
	plan.SigningSecret = state.SigningSecret
	regenerated := false
	if signingSecretRegenerationRequested(plan.SigningSecretVersion, state.SigningSecretVersion) {
		secret, err := r.regenerateSigningSecret(ctx, id)
		if err != nil {
			if ep != nil {
				// The update landed: record it, but keep the old secret and the
				// old version so the next plan asks for the regeneration again.
				plan.SigningSecretVersion = state.SigningSecretVersion
				resp.Diagnostics.Append(applyWebhookResponse(ep, &plan)...)
				resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
			}
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to regenerate the webhook signing secret: %s", err))
			return
		}
		plan.SigningSecret = tfvalue.StringOrNull(secret)
		regenerated = true
	}

	// A regeneration moves updated_at past the update response, so read the
	// endpoint back after one too.
	if ep == nil || regenerated {
		got, err := r.get(ctx, id)
		if err != nil {
			if ep != nil {
				resp.Diagnostics.AddWarning("Webhook not refreshed",
					fmt.Sprintf("The signing secret was regenerated and saved, but reading the endpoint back failed: %s. The next refresh corrects updated_at.", err))
				resp.Diagnostics.Append(applyWebhookResponse(ep, &plan)...)
				resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
				return
			}
			if plan.SigningSecret.Equal(state.SigningSecret) {
				resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read webhook: %s", err))
				return
			}
			// The secret was regenerated and exists nowhere else: save it with
			// the computed values of the prior state rather than lose it.
			plan.UpdatedAt = state.UpdatedAt
			plan.DisabledReason = state.DisabledReason
			resp.Diagnostics.AddWarning("Webhook not refreshed",
				fmt.Sprintf("The signing secret was regenerated and saved, but reading the endpoint back failed: %s. The next refresh corrects the other attributes.", err))
			resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
			return
		}
		ep = got
	}

	resp.Diagnostics.Append(applyWebhookResponse(ep, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// --- Delete ---

func (r *WebhookResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data WebhookResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := doWebhookRequest(ctx, r.client, "DELETE", webhookPath(data.ID.ValueString()), nil)
	switch {
	case err == nil:
	case !isNotFound(err):
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete webhook: %s", err))
	default:
		// A 404 means already deleted only if the API answers at all.
		if lerr := webhooksUnavailable(ctx, r.client); lerr != nil {
			resp.Diagnostics.AddError("Webhooks unavailable", fmt.Sprintf(
				"Deleting webhook %s answered 404, and so did the list of endpoints (%s): the endpoint may still exist "+
					"and receive events.", data.ID.ValueString(), lerr))
		}
	}
}

// --- ImportState ---

// ImportState imports an endpoint by ID. The API never returns the signing
// secret on a read, so it stays null until signing_secret_version changes.
func (r *WebhookResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// ============================================================================
// API calls
// ============================================================================

func (r *WebhookResource) get(ctx context.Context, id string) (*webhookAPIResponse, error) {
	respBytes, err := doWebhookRequest(ctx, r.client, "GET", webhookPath(id), nil)
	if err != nil {
		return nil, err
	}
	var ep webhookAPIResponse
	if err := json.Unmarshal(respBytes, &ep); err != nil {
		return nil, fmt.Errorf("parse webhook response: %w", err)
	}
	return &ep, nil
}

func (r *WebhookResource) update(ctx context.Context, id string, body webhookUpdateRequest) (*webhookAPIResponse, error) {
	respBytes, err := doWebhookRequest(ctx, r.client, "POST", webhookPath(id), body)
	if err != nil {
		return nil, err
	}
	var ep webhookAPIResponse
	if err := json.Unmarshal(respBytes, &ep); err != nil {
		return nil, fmt.Errorf("parse webhook update response: %w", err)
	}
	return &ep, nil
}

func (r *WebhookResource) regenerateSigningSecret(ctx context.Context, id string) (string, error) {
	respBytes, err := doWebhookRequest(ctx, r.client, "POST", webhookPath(id)+"/regenerate_signing_secret", nil)
	if err != nil {
		return "", err
	}
	var out webhookSigningSecretResponse
	if err := json.Unmarshal(respBytes, &out); err != nil {
		return "", fmt.Errorf("parse regenerate signing secret response: %w", err)
	}
	// A 2xx without the secret has already revoked the old one: report it as a
	// failure so Update keeps the old version and the next plan retries.
	if out.SigningSecret == "" {
		return "", errors.New("the regenerate response carries no signing_secret")
	}
	return out.SigningSecret, nil
}

// ============================================================================
// Helper functions
// ============================================================================

// applyWebhookResponse copies an API endpoint into the resource
// model. It never touches signing_secret or signing_secret_version, which no
// read returns.
func applyWebhookResponse(ep *webhookAPIResponse, data *WebhookResourceModel) diag.Diagnostics {
	m, diags := mapWebhookToModel(ep)
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

// buildWebhookUpdate returns the update body: only the fields
// whose planned value differs from state. name and description are Optional
// + Computed, so a null plan (attribute removed) keeps the stored value.
func buildWebhookUpdate(ctx context.Context, plan, state WebhookResourceModel) (webhookUpdateRequest, diag.Diagnostics) {
	var body webhookUpdateRequest
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
		body.State = webhookStateDisabled
		if plan.Enabled.ValueBool() {
			body.State = webhookStateEnabled
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
