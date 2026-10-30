// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package telemetrydestinations

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/booldefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/int64planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
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
var _ resource.Resource = &TelemetryDestinationResource{}
var _ resource.ResourceWithImportState = &TelemetryDestinationResource{}
var _ resource.ResourceWithConfigValidators = &TelemetryDestinationResource{}

func NewTelemetryDestinationResource() resource.Resource {
	return &TelemetryDestinationResource{}
}

// TelemetryDestinationResource manages an OpenTelemetry destination.
type TelemetryDestinationResource struct {
	client *admin.Client
}

// TelemetryDestinationResourceModel is TelemetryDestinationModel plus the
// write-only headers and their rotation trigger. HeadersWO is always null in
// plan and state: its values are read from req.Config only.
type TelemetryDestinationResourceModel struct {
	ID               types.String `tfsdk:"id"`
	DisplayName      types.String `tfsdk:"display_name"`
	EndpointURL      types.String `tfsdk:"endpoint_url"`
	Enabled          types.Bool   `tfsdk:"enabled"`
	DisabledReason   types.String `tfsdk:"disabled_reason"`
	Workspaces       types.Object `tfsdk:"workspaces"`
	Traces           types.Object `tfsdk:"traces"`
	HeadersWO        types.Map    `tfsdk:"headers_wo"`
	HeadersWOVersion types.Int64  `tfsdk:"headers_wo_version"`
	HeaderNames      types.Set    `tfsdk:"header_names"`
	DeliveryHealth   types.Object `tfsdk:"delivery_health"`
	CreatedAt        types.String `tfsdk:"created_at"`
	UpdatedAt        types.String `tfsdk:"updated_at"`
}

var httpsURL = regexp.MustCompile(`^https://`)

// --- Schema ---

func (r *TelemetryDestinationResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_telemetry_destination"
}

func (r *TelemetryDestinationResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages an OpenTelemetry destination via the Admin API (beta, `telemetry-destinations-2026-08-11`): " +
			"an OTLP/HTTP endpoint the API exports Messages API and Managed Agents traces to, for the workspaces it covers. " +
			"Header values are write-only (`headers_wo`): the API never returns them, and they never reach Terraform state. " +
			"Requires `admin_api_key` (or `ANTHROPIC_ADMIN_API_KEY`) to be configured on the provider.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "ID of the destination (`tdst_...`).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"endpoint_url": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Base URL of the collector's OTLP/HTTP endpoint, for example `https://gateway.example.com/otlp`: `https`, a hostname " +
					"rather than an IP address, port 443 if any, no query string, fragment or credentials, at most 2,048 characters. Leave out the " +
					"per-signal path (`/v1/traces`), which the API appends. **Changing the hostname or port removes every registered header**: " +
					"the provider re-sends `headers_wo` in the same request when it is set.",
				Validators: []validator.String{
					stringvalidator.RegexMatches(httpsURL, "must be an HTTPS URL"),
					stringvalidator.LengthAtMost(2048),
				},
			},
			"display_name": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Human-readable name, 1 to 255 characters. Removing it clears the name.",
				Validators:          []validator.String{stringvalidator.LengthBetween(1, 255)},
			},
			"enabled": schema.BoolAttribute{
				Optional: true,
				Computed: true,
				Default:  booldefault.StaticBool(true),
				MarkdownDescription: "Whether the destination receives deliveries (the API's `state`). Default: `true`. The API disables a destination " +
					"on its own after a redirect or 24 hours of failed deliveries (see `disabled_reason`); the next apply then turns it back on. " +
					"Spans produced while it is disabled are never delivered.",
			},
			"disabled_reason": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "Why the destination is disabled: `caller` (through `enabled = false`), `sustained_failure`, `invalid_address` or `redirect`. " +
					"Null while it is enabled.",
			},
			"workspaces": schema.SingleNestedAttribute{
				Required:            true,
				MarkdownDescription: "Which of the organization's workspaces the destination covers. Sent whole on every change.",
				Attributes: map[string]schema.Attribute{
					"type": schema.StringAttribute{
						Required: true,
						MarkdownDescription: "`all` (every workspace, the default workspace and future ones included), `include` (only the listed ones) " +
							"or `exclude` (every workspace but the listed ones).",
						Validators: []validator.String{stringvalidator.OneOf(workspacesAll, workspacesInclude, workspacesExclude)},
					},
					"workspace_ids": schema.SetAttribute{
						Optional:    true,
						ElementType: types.StringType,
						MarkdownDescription: fmt.Sprintf("IDs (`wrkspc_...`) of the workspaces on the list, for `include` and `exclude`; not allowed with `all`. "+
							"At most %d, the API's cap per request. Omit it for an empty list: an `include` destination then covers no workspace, "+
							"an `exclude` one covers them all.", maxWorkspaceIDsPerRequest),
						Validators: []validator.Set{setvalidator.SizeBetween(1, maxWorkspaceIDsPerRequest)},
					},
				},
			},
			"traces": schema.SingleNestedAttribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "Settings for the traces the destination receives. Omit the block to keep the API's values " +
					"(`metadata_only`, enabled, no traces URL, no content bound on create).",
				PlanModifiers: []planmodifier.Object{objectplanmodifier.UseStateForUnknown(), tracesFollowEndpointHost{}},
				Attributes: map[string]schema.Attribute{
					"content_mode": schema.StringAttribute{
						Optional: true,
						Computed: true,
						MarkdownDescription: "`metadata_only` (default: identifiers, timing, status, token counts) or `content` (adds prompts, responses " +
							"and tool inputs and outputs). Organizations with a HIPAA, FERPA or K-12 data-handling restriction export metadata only.",
						PlanModifiers: []planmodifier.String{tracesDefaultString{value: contentModeMetadataOnly}},
						Validators:    []validator.String{stringvalidator.OneOf(contentModeMetadataOnly, contentModeContent)},
					},
					"enabled": schema.BoolAttribute{
						Optional:            true,
						Computed:            true,
						MarkdownDescription: "Whether the destination receives traces. Default: `true`.",
						PlanModifiers:       []planmodifier.Bool{tracesDefaultBool{value: true}},
					},
					"endpoint_url": schema.StringAttribute{
						Optional: true,
						MarkdownDescription: "Full URL of the collector's traces endpoint, when it is not `endpoint_url` plus `/v1/traces`. Same scheme, " +
							"host and port as `endpoint_url`; the API posts to it as given. Removing it goes back to `endpoint_url` plus `/v1/traces`, " +
							"and so does a change of the hostname or port of `endpoint_url` while the traces block is omitted.",
						Validators: []validator.String{
							stringvalidator.RegexMatches(httpsURL, "must be an HTTPS URL"),
							stringvalidator.LengthAtMost(2048),
						},
					},
					"max_content_bytes": schema.Int64Attribute{
						Optional: true,
						Computed: true,
						MarkdownDescription: "Most bytes of content one exported span can hold, applied once `content_mode` is `content`. " +
							"Null means only the API's maximum applies. Removing it keeps the current value.",
						PlanModifiers: []planmodifier.Int64{int64planmodifier.UseStateForUnknown()},
						Validators:    []validator.Int64{int64validator.AtLeast(1)},
					},
				},
			},
			"headers_wo": schema.MapAttribute{
				Optional:    true,
				WriteOnly:   true,
				Sensitive:   true,
				ElementType: types.StringType,
				MarkdownDescription: "Request headers sent with every delivery, typically the collector's authentication header. Write-only: " +
					"the API stores the values encrypted and never returns them, and they never reach Terraform state. Requires " +
					"`headers_wo_version`, and Terraform >= 1.11. At most 20 headers; names compare case-insensitively. Omit it to leave the " +
					"registered headers alone; set it to `{}` to remove them all.",
				Validators: []validator.Map{
					mapvalidator.SizeAtMost(20),
					mapvalidator.KeysAre(stringvalidator.LengthBetween(1, 128)),
				},
			},
			"headers_wo_version": schema.Int64Attribute{
				Optional: true,
				MarkdownDescription: "Rotation trigger for `headers_wo`. Terraform cannot diff write-only values, so change this value to " +
					"re-send the header values. Required when `headers_wo` is set.",
			},
			"header_names": schema.SetAttribute{
				Computed:    true,
				ElementType: types.StringType,
				MarkdownDescription: "Names of the registered headers, as the API returns them. A name added or removed outside Terraform " +
					"shows up as a change, and the next apply re-sends `headers_wo`.",
				PlanModifiers: []planmodifier.Set{headerNamesPlanModifier{}},
			},
			"delivery_health": schema.SingleNestedAttribute{
				Computed:            true,
				MarkdownDescription: "The most recent delivery results.",
				Attributes: map[string]schema.Attribute{
					"last_error": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "Message of the most recent failed delivery, for example `TLS failure` or `HTTP error (status 503)`; null if none failed. A later success does not clear it.",
					},
					"last_error_at": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "RFC 3339 timestamp of the failure `last_error` describes.",
					},
					"last_success_at": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "RFC 3339 timestamp of the most recent successful delivery.",
					},
				},
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the destination was created.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of the last change to the destination's configuration or state.",
			},
		},
	}
}

// --- ConfigValidators ---

func (r *TelemetryDestinationResource) ConfigValidators(_ context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{telemetryDestinationConfigValidator{}}
}

// telemetryDestinationConfigValidator enforces the cross-attribute rules.
type telemetryDestinationConfigValidator struct{}

func (telemetryDestinationConfigValidator) Description(_ context.Context) string {
	return "Requires headers_wo_version with headers_wo, rejects header names that differ only in case, and workspace_ids with type all."
}

func (v telemetryDestinationConfigValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (telemetryDestinationConfigValidator) ValidateResource(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	// Unknown (an unresolved reference at plan time) is neither set nor
	// missing, so it never trips a check.
	isSet := func(v attr.Value) bool { return !v.IsNull() && !v.IsUnknown() }
	isMissing := func(v attr.Value) bool { return v.IsNull() && !v.IsUnknown() }

	var headers types.Map
	var version types.Int64
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("headers_wo"), &headers)...)
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("headers_wo_version"), &version)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Terraform cannot diff a write-only value: without a version to change,
	// editing a header value in the configuration would be a silent no-op.
	if isSet(headers) && isMissing(version) {
		resp.Diagnostics.AddAttributeError(
			path.Root("headers_wo_version"),
			"Missing required attribute",
			"\"headers_wo_version\" must be set when \"headers_wo\" is configured. It is the rotation trigger: Terraform cannot diff "+
				"write-only values, so change it to re-send the header values. Set it to 1 on initial creation.",
		)
	}

	if isSet(headers) {
		seen := map[string]string{}
		for name := range headers.Elements() {
			lower := strings.ToLower(name)
			if other, dup := seen[lower]; dup {
				resp.Diagnostics.AddAttributeError(path.Root("headers_wo"), "Duplicate header name",
					fmt.Sprintf("Header names compare case-insensitively: %q and %q are the same header.", other, name))
			}
			seen[lower] = name
		}
	}

	// Checked independently of the header rules above, so one plan reports
	// every problem.
	var workspaces types.Object
	diags := req.Config.GetAttribute(ctx, path.Root("workspaces"), &workspaces)
	resp.Diagnostics.Append(diags...)
	if diags.HasError() || !isSet(workspaces) {
		return
	}
	var ws workspacesModel
	diags = workspaces.As(ctx, &ws, basetypes.ObjectAsOptions{})
	resp.Diagnostics.Append(diags...)
	if diags.HasError() {
		return
	}
	if isSet(ws.Type) && ws.Type.ValueString() == workspacesAll && isSet(ws.WorkspaceIDs) {
		resp.Diagnostics.AddAttributeError(path.Root("workspaces").AtName("workspace_ids"), "Conflicting attribute",
			"\"workspace_ids\" must not be set when \"type\" is \"all\": such a destination covers every workspace and has no list.")
	}
}

// --- Configure ---

func (r *TelemetryDestinationResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *TelemetryDestinationResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan TelemetryDestinationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Write-only values are only in the configuration, never in the plan.
	var headers types.Map
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("headers_wo"), &headers)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body, diags := buildTelemetryDestinationCreate(ctx, plan, headers)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	respBytes, err := doTelemetryRequest(ctx, r.client, "POST", telemetryDestinationsPath, body)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create telemetry destination: %s", err))
		return
	}
	var created telemetryDestinationAPIResponse
	if err := json.Unmarshal(respBytes, &created); err != nil {
		resp.Diagnostics.AddError("Parse Error", fmt.Sprintf("Unable to parse create telemetry destination response: %s", err))
		return
	}

	data := plan
	resp.Diagnostics.Append(applyTelemetryDestinationResponse(ctx, &created, plan.Workspaces, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// The create body has no state, so enabled = false takes a second call. A
	// destination should start enabled; if the API created it disabled (an
	// invalid_address, say), the same call turns it on as planned.
	if plan.Enabled.IsNull() || plan.Enabled.IsUnknown() || data.Enabled.Equal(plan.Enabled) {
		resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
		return
	}
	want := stateDisabled
	if plan.Enabled.ValueBool() {
		want = stateEnabled
	}
	updated, err := r.update(ctx, created.ID, map[string]any{"state": map[string]any{"type": want}})
	if err == nil {
		resp.Diagnostics.Append(applyTelemetryDestinationResponse(ctx, updated, plan.Workspaces, &data)...)
	}
	// Save the destination as it is so it is not orphaned; on an error
	// Terraform marks it tainted.
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Telemetry destination %s was created but could not be set to state %q: %s", created.ID, want, err))
		return
	}
	addEnabledMismatchError(&resp.Diagnostics, created.ID, plan.Enabled, data)
}

// --- Read ---

func (r *TelemetryDestinationResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data TelemetryDestinationResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	d, ids, err := readTelemetryDestination(ctx, r.client, data.ID.ValueString())
	if err != nil {
		// Only a 404 on the destination itself says it is gone: d is set when
		// that read succeeded and the workspace list failed.
		if d == nil && admin.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read telemetry destination: %s", err))
		return
	}

	m, diags := mapTelemetryDestinationToModel(ctx, d, ids)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	// headers_wo_version is not an API field and keeps its state value (null
	// after an import); headers_wo stays null.
	setFromModel(&data, m)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Update ---

func (r *TelemetryDestinationResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state TelemetryDestinationResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	var headers types.Map
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("headers_wo"), &headers)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := state.ID.ValueString()

	body, diags := buildTelemetryDestinationUpdate(ctx, plan, state, headers)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	var d *telemetryDestinationAPIResponse
	if len(body) > 0 {
		updated, err := r.update(ctx, id, body)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update telemetry destination: %s", err))
			return
		}
		d = updated
	} else {
		// Nothing to send (a version change with no header to send, say): the
		// API refuses an empty update, so read the destination instead.
		got, err := getTelemetryDestination(ctx, r.client, id)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read telemetry destination: %s", err))
			return
		}
		d = got
	}

	data := plan
	resp.Diagnostics.Append(applyTelemetryDestinationResponse(ctx, d, plan.Workspaces, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
	addEnabledMismatchError(&resp.Diagnostics, id, plan.Enabled, data)
}

// addEnabledMismatchError reports a destination the API left in another state
// than the planned one, for example disabled at once over an invalid address.
// The state is saved as the API reports it; the error keeps Terraform from
// taking it for the planned value (an inconsistent result) and names the
// reason.
func addEnabledMismatchError(diags *diag.Diagnostics, id string, planned types.Bool, data TelemetryDestinationResourceModel) {
	if planned.IsNull() || planned.IsUnknown() || data.Enabled.Equal(planned) {
		return
	}
	diags.AddError("Telemetry destination state differs from the plan", fmt.Sprintf(
		"Telemetry destination %s should be enabled = %t, but the API reports enabled = %t (disabled_reason: %s). "+
			"Fix the cause, then apply again.", id, planned.ValueBool(), data.Enabled.ValueBool(), data.DisabledReason))
}

// --- Delete ---

func (r *TelemetryDestinationResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data TelemetryDestinationResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := doTelemetryRequest(ctx, r.client, "DELETE", telemetryDestinationPath(data.ID.ValueString()), nil)
	if err != nil && !admin.IsNotFound(err) {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete telemetry destination: %s", err))
	}
}

// --- ImportState ---

// ImportState imports a destination by ID. Header values cannot be read back:
// leave headers_wo unset to keep the registered headers, or set it with
// headers_wo_version to replace them on the next apply.
func (r *TelemetryDestinationResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (r *TelemetryDestinationResource) update(ctx context.Context, id string, body map[string]any) (*telemetryDestinationAPIResponse, error) {
	respBytes, err := doTelemetryRequest(ctx, r.client, "POST", telemetryDestinationPath(id), body)
	if err != nil {
		return nil, err
	}
	var d telemetryDestinationAPIResponse
	if err := json.Unmarshal(respBytes, &d); err != nil {
		return nil, fmt.Errorf("parse telemetry destination update response: %w", err)
	}
	return &d, nil
}

// ============================================================================
// Helper functions
// ============================================================================

// setFromModel copies the API-derived attributes into the resource model,
// leaving headers_wo (always null) and headers_wo_version alone. endpoint_url
// and traces.endpoint_url keep the spelling data already holds (plan or
// state) when the API returns the same URL normalized (see sameURL).
func setFromModel(data *TelemetryDestinationResourceModel, m TelemetryDestinationModel) {
	if isKnownString(data.EndpointURL) && sameURL(data.EndpointURL.ValueString(), m.EndpointURL.ValueString()) {
		m.EndpointURL = data.EndpointURL
	}
	m.Traces = keepTracesURLSpelling(data.Traces, m.Traces)
	data.ID = m.ID
	data.DisplayName = m.DisplayName
	data.EndpointURL = m.EndpointURL
	data.Enabled = m.Enabled
	data.DisabledReason = m.DisabledReason
	data.Workspaces = m.Workspaces
	data.Traces = m.Traces
	data.HeaderNames = m.HeaderNames
	data.DeliveryHealth = m.DeliveryHealth
	data.CreatedAt = m.CreatedAt
	data.UpdatedAt = m.UpdatedAt
	data.HeadersWO = types.MapNull(types.StringType)
}

// applyTelemetryDestinationResponse maps a create or update response. Those
// responses carry the scope's type only, and the list the request sent
// replaced the whole one, so workspace_ids is taken from the plan rather than
// read back from the list endpoint.
func applyTelemetryDestinationResponse(ctx context.Context, d *telemetryDestinationAPIResponse, planned types.Object, data *TelemetryDestinationResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics
	var ids []string
	if !planned.IsNull() && !planned.IsUnknown() {
		var ws workspacesModel
		diags.Append(planned.As(ctx, &ws, basetypes.ObjectAsOptions{})...)
		if !ws.WorkspaceIDs.IsNull() && !ws.WorkspaceIDs.IsUnknown() {
			diags.Append(ws.WorkspaceIDs.ElementsAs(ctx, &ids, false)...)
		}
	}
	m, dd := mapTelemetryDestinationToModel(ctx, d, ids)
	diags.Append(dd...)
	setFromModel(data, m)
	return diags
}

// buildWorkspacesPayload returns the `workspaces` object of a request. On
// update an omitted workspace_ids keeps the stored list while the type stays
// the same, so include/exclude always send the whole list there, [] for none.
func buildWorkspacesPayload(ctx context.Context, obj types.Object, forUpdate bool) (map[string]any, diag.Diagnostics) {
	var ws workspacesModel
	diags := obj.As(ctx, &ws, basetypes.ObjectAsOptions{})
	if diags.HasError() {
		return nil, diags
	}
	out := map[string]any{"type": ws.Type.ValueString()}
	if ws.Type.ValueString() == workspacesAll {
		return out, diags
	}
	ids := []string{}
	if !ws.WorkspaceIDs.IsNull() && !ws.WorkspaceIDs.IsUnknown() {
		diags.Append(ws.WorkspaceIDs.ElementsAs(ctx, &ids, false)...)
	}
	if len(ids) > 0 || forUpdate {
		slices.Sort(ids)
		out["workspace_ids"] = ids
	}
	return out, diags
}

// buildTracesPayload returns the `traces` object of a request. endpoint_url is
// sent as null to remove it on update; max_content_bytes only when known.
func buildTracesPayload(ctx context.Context, obj types.Object, forUpdate bool) (map[string]any, diag.Diagnostics) {
	var t tracesModel
	diags := obj.As(ctx, &t, basetypes.ObjectAsOptions{})
	if diags.HasError() {
		return nil, diags
	}
	out := map[string]any{}
	if !t.ContentMode.IsNull() && !t.ContentMode.IsUnknown() {
		out["content_mode"] = t.ContentMode.ValueString()
	}
	if !t.Enabled.IsNull() && !t.Enabled.IsUnknown() {
		out["enabled"] = t.Enabled.ValueBool()
	}
	switch {
	case !t.EndpointURL.IsNull() && !t.EndpointURL.IsUnknown():
		out["endpoint_url"] = t.EndpointURL.ValueString()
	case t.EndpointURL.IsNull() && forUpdate:
		out["endpoint_url"] = nil
	}
	if !t.MaxContentBytes.IsNull() && !t.MaxContentBytes.IsUnknown() {
		out["max_content_bytes"] = t.MaxContentBytes.ValueInt64()
	}
	return out, diags
}

// buildTelemetryDestinationCreate returns the create body. headers is the
// configuration's headers_wo.
func buildTelemetryDestinationCreate(ctx context.Context, plan TelemetryDestinationResourceModel, headers types.Map) (map[string]any, diag.Diagnostics) {
	var diags diag.Diagnostics
	body := map[string]any{"endpoint_url": plan.EndpointURL.ValueString()}

	ws, d := buildWorkspacesPayload(ctx, plan.Workspaces, false)
	diags.Append(d...)
	body["workspaces"] = ws

	if !plan.DisplayName.IsNull() && !plan.DisplayName.IsUnknown() {
		body["display_name"] = plan.DisplayName.ValueString()
	}
	// traces is unknown when the block is omitted: the API applies its defaults.
	if !plan.Traces.IsNull() && !plan.Traces.IsUnknown() {
		t, d := buildTracesPayload(ctx, plan.Traces, false)
		diags.Append(d...)
		body["traces"] = t
	}
	if !headers.IsNull() && !headers.IsUnknown() && len(headers.Elements()) > 0 {
		values := map[string]string{}
		diags.Append(headers.ElementsAs(ctx, &values, false)...)
		body["headers"] = values
	}
	return body, diags
}

// buildTelemetryDestinationUpdate returns the update body: the fields that
// changed since state, plus the headers whenever headers_wo is configured.
//
// Re-sending the headers on every update, not only when headers_wo_version
// changes, follows the write-only convention, and it is required whenever
// endpoint_url changes host or port, because the API then removes every
// registered header. A request that sets a header value must also carry
// endpoint_url; the current URL is accepted.
func buildTelemetryDestinationUpdate(ctx context.Context, plan, state TelemetryDestinationResourceModel, headers types.Map) (map[string]any, diag.Diagnostics) {
	var diags diag.Diagnostics
	body := map[string]any{}

	hostChanged := endpointHostOrPortChanged(state.EndpointURL.ValueString(), plan.EndpointURL.ValueString())
	if !plan.EndpointURL.Equal(state.EndpointURL) {
		body["endpoint_url"] = plan.EndpointURL.ValueString()
	}
	if !plan.DisplayName.Equal(state.DisplayName) && !plan.DisplayName.IsUnknown() {
		if plan.DisplayName.IsNull() {
			body["display_name"] = nil
		} else {
			body["display_name"] = plan.DisplayName.ValueString()
		}
	}
	if !plan.Enabled.IsNull() && !plan.Enabled.IsUnknown() && !plan.Enabled.Equal(state.Enabled) {
		stateType := stateDisabled
		if plan.Enabled.ValueBool() {
			stateType = stateEnabled
		}
		body["state"] = map[string]any{"type": stateType}
	}
	if !plan.Workspaces.IsUnknown() && !plan.Workspaces.Equal(state.Workspaces) {
		ws, d := buildWorkspacesPayload(ctx, plan.Workspaces, true)
		diags.Append(d...)
		body["workspaces"] = ws
	}
	// A host or port change must also move traces.endpoint_url to the new host
	// (or clear it) in the same request, so the planned traces go along. With
	// the traces block left out, tracesFollowEndpointHost plans traces unknown
	// when the stored traces URL is on the old host: it is cleared, back to
	// endpoint_url plus /v1/traces, rather than replayed.
	switch {
	case !plan.Traces.IsNull() && !plan.Traces.IsUnknown() && (!plan.Traces.Equal(state.Traces) || hostChanged):
		t, d := buildTracesPayload(ctx, plan.Traces, true)
		diags.Append(d...)
		body["traces"] = t
	case plan.Traces.IsUnknown() && hostChanged && !tracesEndpointURL(state.Traces).IsNull():
		body["traces"] = map[string]any{"endpoint_url": nil}
	}

	patch, d := buildHeadersPatch(ctx, headers, state.HeaderNames)
	diags.Append(d...)
	if patch != nil {
		body["headers"] = patch
		body["endpoint_url"] = plan.EndpointURL.ValueString()
	}
	return body, diags
}

// buildHeadersPatch returns the `headers` merge patch of an update: every
// configured header with its value, plus null for each registered name the
// configuration no longer has. A configured name that matches a registered one
// case-insensitively is sent with the registered spelling, so a re-send never
// renames a header behind header_names' back. It returns nil when headers_wo
// is not configured (the registered headers are left alone) or when there is
// nothing to send.
func buildHeadersPatch(ctx context.Context, headers types.Map, registered types.Set) (map[string]any, diag.Diagnostics) {
	var diags diag.Diagnostics
	if headers.IsNull() || headers.IsUnknown() {
		return nil, diags
	}
	values := map[string]string{}
	diags.Append(headers.ElementsAs(ctx, &values, false)...)
	var names []string
	if !registered.IsNull() && !registered.IsUnknown() {
		diags.Append(registered.ElementsAs(ctx, &names, false)...)
	}
	if diags.HasError() {
		return nil, diags
	}

	byLower := make(map[string]string, len(names))
	for _, n := range names {
		byLower[strings.ToLower(n)] = n
	}
	patch := make(map[string]any, len(values)+len(names))
	for name, value := range values {
		key := name
		if registeredName, ok := byLower[strings.ToLower(name)]; ok {
			key = registeredName
			delete(byLower, strings.ToLower(name))
		}
		patch[key] = value
	}
	for _, n := range byLower {
		patch[n] = nil
	}
	if len(patch) == 0 {
		return nil, diags
	}
	return patch, diags
}

// sameURL reports whether two URLs differ at most in what a server may
// normalize: the letter case of the scheme and host, an explicit default
// port 443, and a trailing slash. Whether the API normalizes endpoint URLs
// is not documented; keeping the configured spelling in that case avoids an
// inconsistent result after apply.
func sameURL(a, b string) bool {
	if a == b {
		return true
	}
	ua, errA := url.Parse(a)
	ub, errB := url.Parse(b)
	if errA != nil || errB != nil || endpointHostOrPortChanged(a, b) {
		return false
	}
	return strings.EqualFold(ua.Scheme, ub.Scheme) &&
		strings.TrimSuffix(ua.EscapedPath(), "/") == strings.TrimSuffix(ub.EscapedPath(), "/") &&
		ua.RawQuery == ub.RawQuery
}

// keepTracesURLSpelling returns got with the traces.endpoint_url of prior when
// both are known and sameURL.
func keepTracesURLSpelling(prior, got types.Object) types.Object {
	if prior.IsNull() || prior.IsUnknown() || got.IsNull() || got.IsUnknown() {
		return got
	}
	priorURL, gotURL := tracesEndpointURL(prior), tracesEndpointURL(got)
	if !isKnownString(priorURL) || !isKnownString(gotURL) || priorURL.Equal(gotURL) || !sameURL(priorURL.ValueString(), gotURL.ValueString()) {
		return got
	}
	attrs := got.Attributes()
	attrs["endpoint_url"] = priorURL
	return types.ObjectValueMust(tracesAttrTypes, attrs)
}

// tracesEndpointURL returns the endpoint_url of a traces object, null when the
// object is null or unknown.
func tracesEndpointURL(traces types.Object) types.String {
	if traces.IsNull() || traces.IsUnknown() {
		return types.StringNull()
	}
	if v, ok := traces.Attributes()["endpoint_url"].(types.String); ok {
		return v
	}
	return types.StringNull()
}

func isKnownString(v types.String) bool {
	return !v.IsNull() && !v.IsUnknown()
}

// endpointHostOrPortChanged reports whether two endpoint URLs differ in
// hostname (case-insensitively) or port, 443 being the https default. An
// unparsable URL counts as a change.
func endpointHostOrPortChanged(before, after string) bool {
	if before == after {
		return false
	}
	b, errB := url.Parse(before)
	a, errA := url.Parse(after)
	if errB != nil || errA != nil {
		return true
	}
	port := func(u *url.URL) string {
		if p := u.Port(); p != "" {
			return p
		}
		return "443"
	}
	return !strings.EqualFold(b.Hostname(), a.Hostname()) || port(b) != port(a)
}

// headerNamesEqualFold reports whether the configured header names and the
// registered ones are the same set, compared case-insensitively.
func headerNamesEqualFold(configured []string, registered []string) bool {
	norm := func(in []string) []string {
		out := make([]string, len(in))
		for i, s := range in {
			out[i] = strings.ToLower(s)
		}
		slices.Sort(out)
		return slices.Compact(out)
	}
	return slices.Equal(norm(configured), norm(registered))
}

// tracesConfigured reports whether the configuration sets the traces block.
func tracesConfigured(ctx context.Context, config tfsdk.Config, diags *diag.Diagnostics) bool {
	var traces types.Object
	diags.Append(config.GetAttribute(ctx, path.Root("traces"), &traces)...)
	return !traces.IsNull() && !traces.IsUnknown()
}

// tracesDefaultString plans a default for a traces attribute left out of a
// configured traces block, and does nothing when the block itself is left
// out (the block then keeps its state value). A schema Default does not work
// here: the framework applies nested defaults inside the prior state's object
// even when the whole block is omitted, which turned every plan of such a
// resource into a spurious update.
type tracesDefaultString struct{ value string }

func (m tracesDefaultString) Description(context.Context) string {
	return fmt.Sprintf("Defaults to %q when the traces block is set without this attribute.", m.value)
}

func (m tracesDefaultString) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (m tracesDefaultString) PlanModifyString(ctx context.Context, req planmodifier.StringRequest, resp *planmodifier.StringResponse) {
	if req.ConfigValue.IsNull() && tracesConfigured(ctx, req.Config, &resp.Diagnostics) {
		resp.PlanValue = types.StringValue(m.value)
	}
}

// tracesDefaultBool is tracesDefaultString for a boolean.
type tracesDefaultBool struct{ value bool }

func (m tracesDefaultBool) Description(context.Context) string {
	return fmt.Sprintf("Defaults to %t when the traces block is set without this attribute.", m.value)
}

func (m tracesDefaultBool) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (m tracesDefaultBool) PlanModifyBool(ctx context.Context, req planmodifier.BoolRequest, resp *planmodifier.BoolResponse) {
	if req.ConfigValue.IsNull() && tracesConfigured(ctx, req.Config, &resp.Diagnostics) {
		resp.PlanValue = types.BoolValue(m.value)
	}
}

// tracesFollowEndpointHost plans traces as unknown when the block is left out
// of the configuration (UseStateForUnknown then plans the stored object), the
// stored traces.endpoint_url is set, and endpoint_url changes hostname or port
// (or is unknown). That URL must share the host and port of endpoint_url, so
// Update clears it, back to endpoint_url plus /v1/traces, instead of
// replaying the stored one, which the API would refuse; the API's values land
// in state. A warning says the custom traces URL goes.
type tracesFollowEndpointHost struct{}

func (tracesFollowEndpointHost) Description(context.Context) string {
	return "Moves an unmanaged traces.endpoint_url along with an endpoint_url host or port change."
}

func (m tracesFollowEndpointHost) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (tracesFollowEndpointHost) PlanModifyObject(ctx context.Context, req planmodifier.ObjectRequest, resp *planmodifier.ObjectResponse) {
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() || !req.ConfigValue.IsNull() || tracesEndpointURL(req.StateValue).IsNull() {
		return
	}
	var planURL, stateURL types.String
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("endpoint_url"), &planURL)...)
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("endpoint_url"), &stateURL)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if !planURL.IsUnknown() && !endpointHostOrPortChanged(stateURL.ValueString(), planURL.ValueString()) {
		return
	}
	resp.PlanValue = types.ObjectUnknown(tracesAttrTypes)
	if !planURL.IsUnknown() {
		resp.Diagnostics.AddAttributeWarning(path.Root("traces"), "Custom traces URL will be reset",
			fmt.Sprintf("traces.endpoint_url (%s) must share the hostname and port of endpoint_url, which changes, and the traces block "+
				"is not set: the update resets it to endpoint_url plus /v1/traces. Set the traces block with an endpoint_url on the new "+
				"host to keep a custom path.", tracesEndpointURL(req.StateValue).ValueString()))
	}
}

// headerNamesPlanModifier plans header_names from state while the registered
// headers stay as they are, and unknown when an apply changes them: when
// headers_wo names other headers than the registered ones (a name added or
// removed in the configuration or outside Terraform, which makes the plan an
// update), or when endpoint_url changes host or port, which removes every
// registered header.
type headerNamesPlanModifier struct{}

func (headerNamesPlanModifier) Description(context.Context) string {
	return "Keeps header_names from state unless the registered headers change."
}

func (m headerNamesPlanModifier) MarkdownDescription(ctx context.Context) string {
	return m.Description(ctx)
}

func (headerNamesPlanModifier) PlanModifySet(ctx context.Context, req planmodifier.SetRequest, resp *planmodifier.SetResponse) {
	// Create (the API reports the names) or destroy.
	if req.State.Raw.IsNull() || req.Plan.Raw.IsNull() {
		return
	}

	var headers types.Map
	var planURL, stateURL types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("headers_wo"), &headers)...)
	resp.Diagnostics.Append(req.Plan.GetAttribute(ctx, path.Root("endpoint_url"), &planURL)...)
	resp.Diagnostics.Append(req.State.GetAttribute(ctx, path.Root("endpoint_url"), &stateURL)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var registered []string
	if !req.StateValue.IsNull() && !req.StateValue.IsUnknown() {
		resp.Diagnostics.Append(req.StateValue.ElementsAs(ctx, &registered, false)...)
	}
	hostChanged := planURL.IsUnknown() || endpointHostOrPortChanged(stateURL.ValueString(), planURL.ValueString())

	switch {
	case headers.IsUnknown():
		resp.PlanValue = types.SetUnknown(types.StringType)
	case headers.IsNull():
		if !hostChanged {
			resp.PlanValue = req.StateValue
			return
		}
		resp.PlanValue = types.SetUnknown(types.StringType)
		if len(registered) > 0 {
			resp.Diagnostics.AddAttributeWarning(path.Root("endpoint_url"), "Registered headers will be removed",
				"Changing the hostname or port of endpoint_url removes every header registered on the telemetry destination, and "+
					"headers_wo is not set, so none will be sent again. Set headers_wo (with headers_wo_version) to keep authenticating to the collector.")
		}
	default:
		configured := make([]string, 0, len(headers.Elements()))
		for name := range headers.Elements() {
			configured = append(configured, name)
		}
		if !hostChanged && headerNamesEqualFold(configured, registered) {
			resp.PlanValue = req.StateValue
			return
		}
		resp.PlanValue = types.SetUnknown(types.StringType)
	}
}
