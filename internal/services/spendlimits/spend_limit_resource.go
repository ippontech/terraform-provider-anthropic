// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package spendlimits

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"

	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &SpendLimitResource{}
var _ resource.ResourceWithImportState = &SpendLimitResource{}
var _ resource.ResourceWithConfigValidators = &SpendLimitResource{}

func NewSpendLimitResource() resource.Resource {
	return &SpendLimitResource{}
}

// SpendLimitResource manages one spend limit, identified by its (scope,
// period) pair. The set endpoint is an upsert on that pair, so Create adopts a
// limit that already exists and Update re-sets the amount in place.
type SpendLimitResource struct {
	client *admin.Client
}

// SpendLimitResourceModel describes the resource data model.
type SpendLimitResourceModel struct {
	ID        types.String `tfsdk:"id"`
	Scope     types.Object `tfsdk:"scope"`
	Period    types.String `tfsdk:"period"`
	Amount    types.String `tfsdk:"amount"`
	Currency  types.String `tfsdk:"currency"`
	IsEnabled types.Bool   `tfsdk:"is_enabled"`
	CreatedAt types.String `tfsdk:"created_at"`
	UpdatedAt types.String `tfsdk:"updated_at"`
}

// spendLimitScopeModel is the writable scope: a user (Claude Enterprise), or
// the organization or one of its workspaces (Claude Console).
type spendLimitScopeModel struct {
	Type        types.String `tfsdk:"type"`
	UserID      types.String `tfsdk:"user_id"`
	WorkspaceID types.String `tfsdk:"workspace_id"`
}

var resourceScopeAttrTypes = map[string]attr.Type{
	"type":         types.StringType,
	"user_id":      types.StringType,
	"workspace_id": types.StringType,
}

// amountPattern is the API's "non-negative integer decimal string", without
// leading zeros so the value reads back as written.
var amountPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)$`)

// --- Schema ---

func (r *SpendLimitResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_spend_limit"
}

func (r *SpendLimitResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Sets a spend limit via the Admin API: a cap on metered spend for one scope and period. " +
			"A Claude Enterprise organization sets `user` limits (its seat-tier, group and organization defaults are configured in claude.ai); " +
			"a Claude Console organization sets `organization` and `workspace` limits, an early access preview granted on request. " +
			"The API upserts on (`scope`, `period`): creating this resource **adopts** a limit that already exists for that pair and overwrites its amount, " +
			"and destroying it deletes that limit, the member or workspace falling back to any inherited limit. " +
			enterpriseKeyNote,
		Attributes: map[string]schema.Attribute{
			// No UseStateForUnknown on id and created_at: an amount change is the
			// same upsert as a create, and it answers with a new limit when the
			// tracked one was deleted since the plan's refresh (a saved plan,
			// -refresh=false, another instance on the pair destroyed first).
			"id": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "ID of the spend limit (`spl_...`). An amount change shows it as known after apply: " +
					"if the limit was deleted outside Terraform since the last refresh, the set creates it again under a new ID.",
			},
			"scope": schema.SingleNestedAttribute{
				Required: true,
				MarkdownDescription: "What the limit applies to. Part of the upsert key with `period`, so changing it forces replacement. " +
					"Any combination other than `user` (Claude Enterprise) or `organization` / `workspace` (Claude Console) is rejected by the API with a 400.",
				PlanModifiers: []planmodifier.Object{objectplanmodifier.RequiresReplace()},
				Attributes: map[string]schema.Attribute{
					"type": schema.StringAttribute{
						Required:            true,
						MarkdownDescription: "Scope type: `user`, `organization` or `workspace`.",
						Validators:          []validator.String{stringvalidator.OneOf(writableScopeTypes...)},
					},
					"user_id": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "ID of the member (`user_...`). Required when `type` is `user`, not allowed otherwise.",
						Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
					},
					"workspace_id": schema.StringAttribute{
						Optional:            true,
						MarkdownDescription: "ID of the workspace (`wrkspc_...`). Required when `type` is `workspace`, not allowed otherwise.",
						Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
					},
				},
			},
			"period": schema.StringAttribute{
				Optional: true,
				Computed: true,
				Default:  stringdefault.StaticString(periodMonthly),
				MarkdownDescription: "Window the limit resets over: `daily`, `weekly` or `monthly` (default). `organization` and `workspace` limits are monthly only. " +
					"Part of the upsert key with `scope`, so changing it forces replacement.",
				Validators:    []validator.String{stringvalidator.OneOf(periods...)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"amount": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Limit as a non-negative integer decimal string in the minor unit of `currency` (cents for USD): `\"50000\"` is $500.00. " +
					"Omit it (or set `null`) on a `user` limit for an explicit no-limit override for this period; `organization` and `workspace` limits always carry an amount. " +
					"Changing it updates the limit in place.",
				Validators: []validator.String{stringvalidator.RegexMatches(amountPattern, "must be a non-negative integer decimal string without leading zeros, in minor currency units")},
			},
			"currency": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "ISO 4217 code of the organization's billing currency, the unit of `amount`.",
			},
			"is_enabled": schema.BoolAttribute{
				Computed: true,
				MarkdownDescription: "Read-only. `false` when extra usage is switched off for the organization (`organization` limit); the amount is kept and applies again " +
					"once it is switched back on. Always `true` for other limits.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the limit was created.",
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the limit was last modified.",
			},
		},
	}
}

func (r *SpendLimitResource) ConfigValidators(_ context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{spendLimitConfigValidator{}}
}

// --- Configure ---

func (r *SpendLimitResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

// --- Create / Update ---

// Create sets the limit. The API upserts on (scope, period), so an existing
// limit for that pair is adopted and its amount overwritten.
func (r *SpendLimitResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan SpendLimitResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.set(ctx, plan, "set", &resp.State, &resp.Diagnostics)
}

// Update re-sets the limit with the new amount: scope and period force
// replacement, so the upsert targets the same row.
func (r *SpendLimitResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan SpendLimitResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.set(ctx, plan, "update", &resp.State, &resp.Diagnostics)
}

func (r *SpendLimitResource) set(ctx context.Context, plan SpendLimitResourceModel, op string, state *tfsdk.State, diags *diag.Diagnostics) {
	body, d := buildSpendLimitSetRequest(ctx, plan)
	diags.Append(d...)
	if diags.HasError() {
		return
	}

	writes := writesFor(r.client)
	defer writes.lock(body.Scope, body.Period)()

	respBody, err := r.client.DoRequest(ctx, "POST", spendLimitsURL(spendLimitsPath, nil), body)
	if err != nil {
		diags.AddError("Client Error", fmt.Sprintf("Unable to %s spend limit: %s", op, err))
		return
	}
	if id := r.setState(ctx, respBody, op, state, diags); id != "" {
		writes.recordSet(id)
	}
}

// --- Read ---

func (r *SpendLimitResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state SpendLimitResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	body, err := r.client.DoRequest(ctx, "GET", spendLimitsURL(spendLimitPath(state.ID.ValueString()), nil), nil)
	if err != nil {
		if admin.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read spend limit: %s", err))
		return
	}
	r.setState(ctx, body, "read", &resp.State, &resp.Diagnostics)
}

// --- Delete ---

// Delete removes the limit; the member or workspace falls back to any
// inherited limit for that period. A 404 means it is already gone. A limit
// that another resource instance set during this apply (the upsert keeps its
// ID) is left in place: that instance now manages it.
func (r *SpendLimitResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state SpendLimitResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	var scope spendLimitScopeModel
	resp.Diagnostics.Append(state.Scope.As(ctx, &scope, basetypes.ObjectAsOptions{UnhandledNullAsEmpty: true, UnhandledUnknownAsEmpty: true})...)
	if resp.Diagnostics.HasError() {
		return
	}

	id := state.ID.ValueString()
	writes := writesFor(r.client)
	defer writes.lock(spendLimitScopeAPI{
		Type:        scope.Type.ValueString(),
		UserID:      scope.UserID.ValueString(),
		WorkspaceID: scope.WorkspaceID.ValueString(),
	}, state.Period.ValueString())()
	if writes.wasSet(id) {
		resp.Diagnostics.AddWarning("Spend Limit Not Deleted",
			fmt.Sprintf("Spend limit %s was set again during this apply by another anthropic_spend_limit with the same scope and "+
				"period (the API upserts on that pair and keeps the ID), which now manages it. It was left in place and only "+
				"removed from this resource's state.", id))
		return
	}

	_, err := r.client.DoRequest(ctx, "DELETE", spendLimitsURL(spendLimitPath(id), nil), nil)
	if err != nil && !admin.IsNotFound(err) {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to delete spend limit: %s", err))
	}
}

// --- ImportState ---

func (r *SpendLimitResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// ============================================================================
// Helper functions
// ============================================================================

// buildSpendLimitSetRequest builds the set body from the plan. A null amount
// is sent as JSON null: the explicit no-limit override.
func buildSpendLimitSetRequest(ctx context.Context, plan SpendLimitResourceModel) (spendLimitSetRequest, diag.Diagnostics) {
	var scope spendLimitScopeModel
	diags := plan.Scope.As(ctx, &scope, basetypes.ObjectAsOptions{})
	if diags.HasError() {
		return spendLimitSetRequest{}, diags
	}

	body := spendLimitSetRequest{
		Scope: spendLimitScopeAPI{
			Type:        scope.Type.ValueString(),
			UserID:      scope.UserID.ValueString(),
			WorkspaceID: scope.WorkspaceID.ValueString(),
		},
		Period: plan.Period.ValueString(),
	}
	if !plan.Amount.IsNull() {
		amount := plan.Amount.ValueString()
		body.Amount = &amount
	}
	return body, diags
}

// setState decodes a spend limit response and writes it to state. It returns
// the limit's ID, or "" when nothing was written.
func (r *SpendLimitResource) setState(ctx context.Context, body []byte, op string, state *tfsdk.State, diags *diag.Diagnostics) string {
	var limit spendLimitAPIResponse
	if err := json.Unmarshal(body, &limit); err != nil {
		diags.AddError("Parse Error", fmt.Sprintf("Unable to parse %s spend limit response: %s", op, err))
		return ""
	}
	model, d := mapSpendLimitToState(limit)
	diags.Append(d...)
	if diags.HasError() {
		return ""
	}
	diags.Append(state.Set(ctx, &model)...)
	return limit.ID
}

// mapSpendLimitToState maps an API spend limit to the resource model. The
// scope keeps only the identifiers a writable scope carries; an imported
// limit of a read-only scope type (seat_tier, rbac_group, ...) maps its type
// alone and then plans a replacement the API will reject, which is why such
// limits cannot be managed here.
func mapSpendLimitToState(limit spendLimitAPIResponse) (SpendLimitResourceModel, diag.Diagnostics) {
	scope, diags := types.ObjectValue(resourceScopeAttrTypes, map[string]attr.Value{
		"type":         types.StringValue(limit.Scope.Type),
		"user_id":      tfvalue.StringOrNull(limit.Scope.UserID),
		"workspace_id": tfvalue.StringOrNull(limit.Scope.WorkspaceID),
	})
	return SpendLimitResourceModel{
		ID:        types.StringValue(limit.ID),
		Scope:     scope,
		Period:    types.StringValue(limit.Period),
		Amount:    stringPtrOrNull(limit.Amount),
		Currency:  tfvalue.StringOrNull(limit.Currency),
		IsEnabled: types.BoolValue(limit.IsEnabled),
		CreatedAt: tfvalue.StringOrNull(limit.CreatedAt),
		UpdatedAt: tfvalue.StringOrNull(limit.UpdatedAt),
	}, diags
}

// --- Config validator ---

// spendLimitConfigValidator checks the scope's identifier against its type,
// and the constraints of Console scopes (monthly, with an amount). Unknown
// values are treated as neither set nor missing.
type spendLimitConfigValidator struct{}

func (v spendLimitConfigValidator) Description(_ context.Context) string {
	return "scope identifiers match the scope type; organization and workspace limits are monthly and carry an amount"
}

func (v spendLimitConfigValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func isSet(v attr.Value) bool     { return !v.IsNull() && !v.IsUnknown() }
func isMissing(v attr.Value) bool { return v.IsNull() && !v.IsUnknown() }

func (v spendLimitConfigValidator) ValidateResource(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var config SpendLimitResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() || !isSet(config.Scope) {
		return
	}

	var scope spendLimitScopeModel
	resp.Diagnostics.Append(config.Scope.As(ctx, &scope, basetypes.ObjectAsOptions{UnhandledUnknownAsEmpty: true})...)
	if resp.Diagnostics.HasError() || !isSet(scope.Type) {
		return
	}
	scopeType := scope.Type.ValueString()
	scopePath := path.Root("scope")

	requireOnly := func(want string, value types.String, name string) {
		switch {
		case scopeType == want && isMissing(value):
			resp.Diagnostics.AddAttributeError(scopePath.AtName(name), "Missing scope identifier",
				fmt.Sprintf("scope.%s is required when scope.type is %q.", name, want))
		case scopeType != want && isSet(value):
			resp.Diagnostics.AddAttributeError(scopePath.AtName(name), "Unexpected scope identifier",
				fmt.Sprintf("scope.%s is only allowed when scope.type is %q.", name, want))
		}
	}
	requireOnly(scopeUser, scope.UserID, "user_id")
	requireOnly(scopeWorkspace, scope.WorkspaceID, "workspace_id")

	if scopeType == scopeOrganization || scopeType == scopeWorkspace {
		if isSet(config.Period) && config.Period.ValueString() != periodMonthly {
			resp.Diagnostics.AddAttributeError(path.Root("period"), "Unsupported period",
				fmt.Sprintf("%s limits are monthly only.", scopeType))
		}
		if isMissing(config.Amount) {
			resp.Diagnostics.AddAttributeError(path.Root("amount"), "Missing amount",
				fmt.Sprintf("%s limits always carry an amount; only a user limit can be an explicit no-limit override.", scopeType))
		}
	}
}
