// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package externalkeys

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &ExternalKeyResource{}
var _ resource.ResourceWithImportState = &ExternalKeyResource{}
var _ resource.ResourceWithModifyPlan = &ExternalKeyResource{}

func NewExternalKeyResource() resource.Resource {
	return &ExternalKeyResource{}
}

// ExternalKeyResource defines the resource implementation.
type ExternalKeyResource struct {
	client *admin.Client
}

// --- Schema ---

func (r *ExternalKeyResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_external_key"
}

func (r *ExternalKeyResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Registers a customer-managed encryption key (CMEK) with the organization via the Admin API: " +
			"a record that points to a key in AWS KMS, Google Cloud KMS or Azure Key Vault. Attach it to a workspace " +
			"with `anthropic_workspace.external_key_id`. Requires `admin_api_key` (or `ANTHROPIC_ADMIN_API_KEY`) and CMEK " +
			"turned on for the organization. Once any workspace, even an archived one, uses the key, its `geo` and " +
			"`provider_config` can no longer change and the API refuses to delete it.",
		Attributes: map[string]schema.Attribute{
			// --- Writable ---
			"display_name": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Human-friendly name (1 to 255 characters). Always editable; removing it clears the name.",
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 255)},
			},
			"geo": schema.StringAttribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "Data residency geo that selects the regional validator for this key. Only `us` is supported today; " +
					"the API applies its default when omitted. Editable while the key is `unattached`: changing it on an attached key fails at plan time.",
				Validators:    []validator.String{stringvalidator.LengthAtLeast(1)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"provider_config": schema.SingleNestedAttribute{
				Required: true,
				MarkdownDescription: "Where the key lives in your KMS. `type` selects the provider and which of the other attributes apply. " +
					"Editable while the key is `unattached`, provider included: once a workspace uses the key, any change fails at plan time, " +
					"because data already encrypted needs the original key to be decrypted. The deprecated `role_arn` is ignored by the API and not exposed.",
				Validators: []validator.Object{providerConfigValidator{}},
				Attributes: map[string]schema.Attribute{
					"type": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "KMS provider: `aws`, `gcp` or `azure`.",
						Validators:          []validator.String{stringvalidator.OneOf("aws", "gcp", "azure")},
					},
					"kms_arn": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: kmsARNDescription + " Required when `type` is `aws`.",
						Validators:          []validator.String{stringvalidator.LengthBetween(1, 2048)},
					},
					// An empty string would be sent as is and come back null
					// (or, for region, derived): an inconsistent result, so
					// every attribute below rejects it at plan time.
					"region": schema.StringAttribute{
						Optional:            true,
						Computed:            true,
						MarkdownDescription: regionDescription,
						Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
						PlanModifiers:       []planmodifier.String{regionFollowsKMSARN{}},
					},
					"key_name": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: keyNameDescription + " Required when `type` is `gcp` or `azure`.",
						Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
					},
					"vault_uri": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: vaultURIDescription + " Required when `type` is `azure`.",
						Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
					},
					"tenant_id": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: tenantIDDescription + " Required when `type` is `azure`.",
						Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
					},
					"client_id": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: clientIDDescription,
						Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
					},
				},
			},

			// --- Computed ---
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: idDescription,
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown(), idFollowsKMSARN{}},
			},
			// No UseStateForUnknown: a workspace can attach the key between
			// two applies, so the value an update returns is not the prior one.
			"attachment": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: attachmentDescription,
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the key was registered.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of the last update.",
			},
		},
	}
}

// --- Configure ---

func (r *ExternalKeyResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// ModifyPlan turns the API's attachment rules into plan-time diagnostics.
//
// `geo` and `provider_config` stay editable until a workspace uses the key,
// then freeze. RequiresReplace is no answer once attached: the replacement
// would have to delete the old key, which the API refuses for an attached key
// (archived workspaces included), so the apply would fail half-way, after
// creating an orphan key under create_before_destroy. Leaving it to the API
// would show an in-place update that can only fail. An error at plan time
// says so before anything runs. The check reads the attachment from state,
// which the plan's refresh has just updated; under -refresh=false the API
// rejection still surfaces at apply.
//
// A destroy plan for an attached key gets a warning, because the DELETE will
// be refused.
func (r *ExternalKeyResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {
	if req.State.Raw.IsNull() {
		return
	}

	var state ExternalKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() || state.Attachment.ValueString() != attachmentAttached {
		return
	}

	if req.Plan.Raw.IsNull() {
		resp.Diagnostics.AddWarning("External key is attached to a workspace", fmt.Sprintf(
			"External key %s is used by at least one workspace, so the API will refuse to delete it and the destroy will fail. "+
				"Archived workspaces count, and a key cannot be detached from a workspace.\n\n%s",
			state.ID.ValueString(), stopManagingHint))
		return
	}

	var plan ExternalKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	changed, diags := frozenAttributesChanged(ctx, plan, state)
	resp.Diagnostics.Append(diags...)
	if len(changed) == 0 {
		if unknown := unknownFrozenAttributes(ctx, plan); len(unknown) > 0 {
			resp.Diagnostics.AddWarning("Attached external key: value known only at apply", fmt.Sprintf(
				"External key %s is used by at least one workspace, so the API refuses any change to %s. "+
					"Their planned value is known only at apply (it comes from a resource being created or replaced): "+
					"if it differs from the current one, the update will fail at apply, after the resources it depends on have changed.",
				state.ID.ValueString(), strings.Join(unknown, ", ")))
		}
		return
	}
	resp.Diagnostics.AddError("Cannot change an attached external key", fmt.Sprintf(
		"External key %s is used by at least one workspace, live or archived, so the API no longer accepts changes to %s: "+
			"data already encrypted needs the original key to be decrypted. Only `display_name` can still change.\n\n"+
			"Revert the change, or register the new key as a separate anthropic_external_key and use it from a new workspace "+
			"(a workspace cannot switch keys).",
		state.ID.ValueString(), strings.Join(changed, ", ")))
}

const stopManagingHint = "To stop managing the key with Terraform without deleting it, remove it from the state instead: " +
	"a `removed` block with `lifecycle { destroy = false }`, or `terraform state rm`."

// --- Create ---

func (r *ExternalKeyResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan ExternalKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body, diags := buildExternalKeyCreateBody(ctx, plan)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	respBytes, err := r.client.DoRequest(ctx, "POST", externalKeysPath, body)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create external key: %s", err))
		return
	}

	r.setStateFromResponse(ctx, respBytes, "create", plan.ProviderConfig, &resp.State, &resp.Diagnostics)
}

// --- Read ---

func (r *ExternalKeyResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state ExternalKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	respBytes, err := r.client.DoRequest(ctx, "GET", externalKeyPath(state.ID.ValueString()), nil)
	if err != nil {
		if admin.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read external key: %s", err))
		return
	}

	r.setStateFromResponse(ctx, respBytes, "read", state.ProviderConfig, &resp.State, &resp.Diagnostics)
}

// --- Update ---

func (r *ExternalKeyResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state ExternalKeyModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body, diags := buildExternalKeyUpdateBody(ctx, plan, state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// A value unknown at plan can resolve to the stored one, leaving nothing
	// to send. The reference documents no empty update, and the sibling
	// workspace update answers one with a 400, so the key is read instead.
	method, op, reqBody := "POST", "update", any(body)
	if len(body) == 0 {
		method, op, reqBody = "GET", "read", nil
	}
	respBytes, err := r.client.DoRequest(ctx, method, externalKeyPath(state.ID.ValueString()), reqBody)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to %s external key: %s", op, err))
		return
	}

	r.setStateFromResponse(ctx, respBytes, op, plan.ProviderConfig, &resp.State, &resp.Diagnostics)
	if !resp.Diagnostics.HasError() && plan.DisplayName.IsNull() && !state.DisplayName.IsNull() {
		resp.Diagnostics.Append(checkDisplayNameCleared(ctx, resp.State, state.ID.ValueString())...)
	}
}

// checkDisplayNameCleared reports an error when the API kept the display_name
// that the update sent as null to clear it. Whether the API clears a name on
// null is not verified live; if it does not, an error that says so beats the
// "inconsistent result after apply" Terraform would report for a planned
// null. The state holds the API's name, so the next plan shows the diff again.
func checkDisplayNameCleared(ctx context.Context, state tfsdk.State, id string) diag.Diagnostics {
	var got types.String
	diags := state.GetAttribute(ctx, path.Root("display_name"), &got)
	if diags.HasError() || got.IsNull() {
		return diags
	}
	diags.AddError("External key display_name not cleared", fmt.Sprintf(
		"The update sent display_name = null to clear the name of external key %s, but the API kept %q. "+
			"Set display_name in the configuration to keep a name.", id, got.ValueString()))
	return diags
}

// --- Delete ---

// Delete removes the key record. The API refuses it while any workspace,
// archived ones included, uses the key; the failure is then explained with the
// attachment read back from the API rather than the bare 4xx.
func (r *ExternalKeyResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state ExternalKeyModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := state.ID.ValueString()
	_, err := r.client.DoRequest(ctx, "DELETE", externalKeyPath(id), nil)
	if err == nil || admin.IsNotFound(err) {
		return
	}

	if r.isAttached(ctx, id) {
		resp.Diagnostics.AddError("External key is attached to a workspace", fmt.Sprintf(
			"The API refused to delete external key %s because at least one workspace uses it to encrypt its data. "+
				"Archived workspaces count, and a key cannot be detached from a workspace.\n\n%s\n\nAPI error: %s",
			id, stopManagingHint, err))
		return
	}
	resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete external key: %s", err))
}

// isAttached reports whether the API currently reports the key as attached.
// Any read failure answers false, so the caller falls back to the plain error.
func (r *ExternalKeyResource) isAttached(ctx context.Context, id string) bool {
	respBytes, err := r.client.DoRequest(ctx, "GET", externalKeyPath(id), nil)
	if err != nil {
		return false
	}
	var key externalKeyAPIResponse
	if json.Unmarshal(respBytes, &key) != nil {
		return false
	}
	return key.Attachment != nil && key.Attachment.Type == attachmentAttached
}

// --- ImportState ---

// ImportState accepts either ID form: `ekey_...`, or the KMS key ARN on
// Claude Platform on AWS.
func (r *ExternalKeyResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// ============================================================================
// Helper functions
// ============================================================================

// setStateFromResponse decodes an external key payload and stores it as state.
// prior is the provider_config the request was built from: the plan on create
// and update, the state on read.
func (r *ExternalKeyResource) setStateFromResponse(ctx context.Context, respBytes []byte, op string, prior types.Object, state *tfsdk.State, diags *diag.Diagnostics) {
	var key externalKeyAPIResponse
	if err := json.Unmarshal(respBytes, &key); err != nil {
		diags.AddError("Parse Error", fmt.Sprintf("Unable to parse %s external key response: %s", op, err))
		return
	}

	data, d := mapExternalKeyToModel(ctx, &key)
	diags.Append(d...)
	if diags.HasError() {
		return
	}
	diags.Append(keepPriorVaultURI(ctx, &data, prior)...)
	if diags.HasError() {
		return
	}
	diags.Append(state.Set(ctx, &data)...)
}

// keepPriorVaultURI keeps the vault_uri of prior when the API returns the
// same URI up to a trailing slash or letter case. The Azure portal shows the
// Key Vault URI with a trailing slash, the reference documents it without,
// and whether the API normalizes it is not documented: writing the API's
// spelling over the configured one would be an inconsistent result after
// apply, then a diff on every plan that an attached key cannot apply.
func keepPriorVaultURI(ctx context.Context, data *ExternalKeyModel, prior types.Object) diag.Diagnostics {
	priorPC, diags := providerConfigFromObject(ctx, prior)
	gotPC, d := providerConfigFromObject(ctx, data.ProviderConfig)
	diags.Append(d...)
	if diags.HasError() || !isKnown(priorPC.VaultURI) || !isKnown(gotPC.VaultURI) || priorPC.VaultURI.Equal(gotPC.VaultURI) ||
		!strings.EqualFold(strings.TrimSuffix(priorPC.VaultURI.ValueString(), "/"), strings.TrimSuffix(gotPC.VaultURI.ValueString(), "/")) {
		return diags
	}
	gotPC.VaultURI = priorPC.VaultURI
	data.ProviderConfig, d = types.ObjectValueFrom(ctx, providerConfigAttrTypes, gotPC)
	diags.Append(d...)
	return diags
}

// providerConfigFromObject decodes the provider_config object of a model.
func providerConfigFromObject(ctx context.Context, obj types.Object) (externalKeyProviderConfigModel, diag.Diagnostics) {
	var pc externalKeyProviderConfigModel
	if obj.IsNull() || obj.IsUnknown() {
		return pc, nil
	}
	diags := obj.As(ctx, &pc, basetypes.ObjectAsOptions{})
	return pc, diags
}

// isKnown reports whether v holds a concrete value.
func isKnown(v types.String) bool {
	return !v.IsNull() && !v.IsUnknown()
}

// providerConfigBody builds the provider_config union member the API expects:
// `type` plus every known attribute. An unknown `region` (the plan leaves it
// unknown when `kms_arn` changes) is omitted so the API derives it. A
// `client_id` cleared from an azure key that stays azure is sent as an
// explicit null; on a switch to aws or gcp the new member replaces the whole
// configuration, and client_id does not belong to it.
func providerConfigBody(pc externalKeyProviderConfigModel, prior *externalKeyProviderConfigModel) map[string]any {
	body := map[string]any{"type": pc.Type.ValueString()}
	for name, v := range map[string]types.String{
		"kms_arn":   pc.KMSARN,
		"region":    pc.Region,
		"key_name":  pc.KeyName,
		"vault_uri": pc.VaultURI,
		"tenant_id": pc.TenantID,
		"client_id": pc.ClientID,
	} {
		if isKnown(v) {
			body[name] = v.ValueString()
		}
	}
	if prior != nil && pc.Type.ValueString() == "azure" && prior.Type.Equal(pc.Type) &&
		pc.ClientID.IsNull() && isKnown(prior.ClientID) {
		body["client_id"] = nil
	}
	return body
}

// buildExternalKeyCreateBody builds the POST /v1/organizations/external_keys
// body; `display_name` and `geo` are omitted when not set so the API defaults
// apply.
func buildExternalKeyCreateBody(ctx context.Context, plan ExternalKeyModel) (map[string]any, diag.Diagnostics) {
	pc, diags := providerConfigFromObject(ctx, plan.ProviderConfig)
	if diags.HasError() {
		return nil, diags
	}

	body := map[string]any{"provider_config": providerConfigBody(pc, nil)}
	if isKnown(plan.DisplayName) {
		body["display_name"] = plan.DisplayName.ValueString()
	}
	if isKnown(plan.Geo) {
		body["geo"] = plan.Geo.ValueString()
	}
	return body, diags
}

// buildExternalKeyUpdateBody builds the partial update. Only the fields that
// changed since state are sent: the API leaves omitted fields unchanged, and a
// `display_name` edit on an attached key must not carry a `geo` or
// `provider_config`, which the API refuses there (whether it would accept an
// identical value is not documented). A `display_name` removed from the
// configuration is sent as an explicit null to clear it.
func buildExternalKeyUpdateBody(ctx context.Context, plan, state ExternalKeyModel) (map[string]any, diag.Diagnostics) {
	body := map[string]any{}

	if !plan.DisplayName.IsUnknown() && !plan.DisplayName.Equal(state.DisplayName) {
		if plan.DisplayName.IsNull() {
			body["display_name"] = nil
		} else {
			body["display_name"] = plan.DisplayName.ValueString()
		}
	}
	if isKnown(plan.Geo) && !plan.Geo.Equal(state.Geo) {
		body["geo"] = plan.Geo.ValueString()
	}

	planPC, diags := providerConfigFromObject(ctx, plan.ProviderConfig)
	statePC, d := providerConfigFromObject(ctx, state.ProviderConfig)
	diags.Append(d...)
	if diags.HasError() {
		return nil, diags
	}
	if len(changedProviderConfigFields(planPC, statePC)) > 0 {
		body["provider_config"] = providerConfigBody(planPC, &statePC)
	}
	return body, diags
}

// changedProviderConfigFields lists the provider_config attributes whose
// planned value is known and differs from state. An unknown value is skipped:
// it is either a reference resolved at apply, or `region` left for the API to
// derive, which only happens when `kms_arn` itself changed.
func changedProviderConfigFields(plan, state externalKeyProviderConfigModel) []string {
	var changed []string
	for _, f := range []struct {
		name        string
		plan, state types.String
	}{
		{"type", plan.Type, state.Type},
		{"kms_arn", plan.KMSARN, state.KMSARN},
		{"region", plan.Region, state.Region},
		{"key_name", plan.KeyName, state.KeyName},
		{"vault_uri", plan.VaultURI, state.VaultURI},
		{"tenant_id", plan.TenantID, state.TenantID},
		{"client_id", plan.ClientID, state.ClientID},
	} {
		if !f.plan.IsUnknown() && !f.plan.Equal(f.state) {
			changed = append(changed, "provider_config."+f.name)
		}
	}
	return changed
}

// frozenAttributesChanged lists the attributes an attached key no longer
// accepts that the plan changes: `geo` and the provider_config attributes.
func frozenAttributesChanged(ctx context.Context, plan, state ExternalKeyModel) ([]string, diag.Diagnostics) {
	var changed []string
	if !plan.Geo.IsUnknown() && !plan.Geo.Equal(state.Geo) {
		changed = append(changed, "geo")
	}

	planPC, diags := providerConfigFromObject(ctx, plan.ProviderConfig)
	statePC, d := providerConfigFromObject(ctx, state.ProviderConfig)
	diags.Append(d...)
	if diags.HasError() || plan.ProviderConfig.IsUnknown() {
		return changed, diags
	}
	return append(changed, changedProviderConfigFields(planPC, statePC)...), diags
}

// unknownFrozenAttributes lists the attributes an attached key no longer
// accepts whose planned value is unknown, so that frozenAttributesChanged
// cannot tell whether they change. region is left out: it is unknown only
// when kms_arn or type is, or while the API derives it.
func unknownFrozenAttributes(ctx context.Context, plan ExternalKeyModel) []string {
	var unknown []string
	if plan.Geo.IsUnknown() {
		unknown = append(unknown, "geo")
	}
	if plan.ProviderConfig.IsUnknown() {
		return append(unknown, "provider_config")
	}
	pc, diags := providerConfigFromObject(ctx, plan.ProviderConfig)
	if diags.HasError() {
		return unknown
	}
	for _, f := range []struct {
		name  string
		value types.String
	}{
		{"type", pc.Type}, {"kms_arn", pc.KMSARN}, {"key_name", pc.KeyName},
		{"vault_uri", pc.VaultURI}, {"tenant_id", pc.TenantID}, {"client_id", pc.ClientID},
	} {
		if f.value.IsUnknown() {
			unknown = append(unknown, "provider_config."+f.name)
		}
	}
	return unknown
}

// --- provider_config validation ---

// providerConfigFieldRules lists, per provider type, the attributes that must
// be set; every other provider-specific attribute is allowed only when listed
// in optional.
var providerConfigFieldRules = map[string]struct {
	required []string
	optional []string
}{
	"aws":   {required: []string{"kms_arn"}, optional: []string{"region"}},
	"gcp":   {required: []string{"key_name"}},
	"azure": {required: []string{"key_name", "vault_uri", "tenant_id"}, optional: []string{"client_id"}},
}

// providerConfigValidator checks that provider_config carries the attributes
// its `type` needs and none that belong to another provider. Unknown values
// count as neither set nor missing (see CLAUDE.md, ConfigValidator Unknown
// handling).
type providerConfigValidator struct{}

func (providerConfigValidator) Description(context.Context) string {
	return "provider_config attributes must match its type"
}

func (v providerConfigValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (providerConfigValidator) ValidateObject(ctx context.Context, req validator.ObjectRequest, resp *validator.ObjectResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}

	var pc externalKeyProviderConfigModel
	resp.Diagnostics.Append(req.ConfigValue.As(ctx, &pc, basetypes.ObjectAsOptions{})...)
	if resp.Diagnostics.HasError() || !isKnown(pc.Type) {
		return
	}
	providerType := pc.Type.ValueString()
	rule, ok := providerConfigFieldRules[providerType]
	if !ok {
		return // rejected by the OneOf validator on `type`
	}

	allowed := map[string]bool{}
	for _, n := range append(append([]string{}, rule.required...), rule.optional...) {
		allowed[n] = true
	}
	required := map[string]bool{}
	for _, n := range rule.required {
		required[n] = true
	}

	// Stable order so emitted diagnostics are deterministic.
	for _, f := range []struct {
		name  string
		value types.String
	}{
		{"kms_arn", pc.KMSARN},
		{"region", pc.Region},
		{"key_name", pc.KeyName},
		{"vault_uri", pc.VaultURI},
		{"tenant_id", pc.TenantID},
		{"client_id", pc.ClientID},
	} {
		switch {
		case required[f.name] && f.value.IsNull() && !f.value.IsUnknown():
			resp.Diagnostics.AddAttributeError(req.Path.AtName(f.name), "Missing required attribute",
				fmt.Sprintf("%q is required when provider_config.type is %q.", f.name, providerType))
		case !allowed[f.name] && isKnown(f.value):
			resp.Diagnostics.AddAttributeError(req.Path.AtName(f.name), "Conflicting attribute",
				fmt.Sprintf("%q must not be set when provider_config.type is %q.", f.name, providerType))
		}
	}
}

// --- id plan modifier ---

// idFollowsKMSARN plans id as unknown when it is the key's KMS ARN, the ID
// form on Claude Platform on AWS, and kms_arn changes: whether the API then
// keeps the old ARN as the ID or moves it to the new one is not documented,
// and planning the old ARN would make a new one an inconsistent result.
type idFollowsKMSARN struct{}

func (idFollowsKMSARN) Description(context.Context) string {
	return "Plans an ARN-form ID as unknown when kms_arn changes."
}

func (m idFollowsKMSARN) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (idFollowsKMSARN) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() || !isKnown(req.StateValue) {
		return
	}
	arn := path.Root("provider_config").AtName("kms_arn")
	var stateARN, planARN types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, arn, &stateARN)...)
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, arn, &planARN)...)
	if resp.Diagnostics.HasError() || !isKnown(stateARN) || req.StateValue.ValueString() != stateARN.ValueString() || planARN.Equal(stateARN) {
		return
	}
	resp.PlanValue = types.StringUnknown()
}

// --- region plan modifier ---

// regionFollowsKMSARN plans provider_config.region, which the API derives from
// `kms_arn` when it is not configured. It is null for a non-AWS provider, the
// prior value while `type` and `kms_arn` are unchanged, and unknown otherwise
// (create, or a new key ARN) so the derived region can land without an
// inconsistent-result error. A configured value always wins.
type regionFollowsKMSARN struct{}

func (regionFollowsKMSARN) Description(context.Context) string {
	return "Keeps the derived AWS region while kms_arn is unchanged."
}

func (m regionFollowsKMSARN) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (regionFollowsKMSARN) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if !req.ConfigValue.IsNull() || !req.PlanValue.IsUnknown() {
		return
	}

	parent := req.Path.ParentPath()
	var planType, planARN types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, parent.AtName("type"), &planType)...)
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, parent.AtName("kms_arn"), &planARN)...)
	if resp.Diagnostics.HasError() || planType.IsUnknown() {
		return
	}
	if planType.ValueString() != "aws" {
		resp.PlanValue = types.StringNull()
		return
	}
	if req.State.Raw.IsNull() {
		return // create: the API derives it
	}

	var stateType, stateARN types.String
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, parent.AtName("type"), &stateType)...)
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, parent.AtName("kms_arn"), &stateARN)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if planType.Equal(stateType) && planARN.Equal(stateARN) {
		resp.PlanValue = req.StateValue
	}
}
