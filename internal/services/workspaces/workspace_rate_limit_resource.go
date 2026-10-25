// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/mapvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/objectplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &WorkspaceRateLimitResource{}
var _ resource.ResourceWithImportState = &WorkspaceRateLimitResource{}

func NewWorkspaceRateLimitResource() resource.Resource {
	return &WorkspaceRateLimitResource{}
}

// WorkspaceRateLimitResource manages a workspace's rate-limit overrides for
// one rate-limit group, through
// POST /v1/organizations/workspaces/{workspace_id}/rate_limits/{rate_limit_id}.
// That endpoint only touches the limiter types it names (an integer sets the
// override, null clears it), so the resource owns exactly the types in its
// limits map: other types of the same group, set elsewhere, are left alone.
type WorkspaceRateLimitResource struct {
	adminClient *admin.Client
}

// --- Terraform data models ---

type WorkspaceRateLimitResourceModel struct {
	ID          types.String `tfsdk:"id"`
	WorkspaceID types.String `tfsdk:"workspace_id"`
	RateLimitID types.String `tfsdk:"rate_limit_id"`
	Limits      types.Map    `tfsdk:"limits"`
	Group       types.Object `tfsdk:"group"`
}

// workspaceRateLimitTypes are the limiter types the set-overrides endpoint
// accepts (WorkspaceRateLimitType in the reference).
var workspaceRateLimitTypes = []string{
	"fast_itpmca",
	"fast_otpm",
	"input_tokens_per_minute",
	"input_tokens_per_minute_cache_aware",
	"output_tokens_per_minute",
	"requests_per_minute",
	"tokens_per_day",
	"tokens_per_minute",
	"tool_uses_per_second",
}

// maxWorkspaceRateLimitValue is the endpoint's documented maximum (2^53 - 1).
const maxWorkspaceRateLimitValue = 9007199254740991

// --- Admin API request types ---

// workspaceRateLimitSetValue is one entry of the request body. A nil Value is
// marshalled as JSON null, which clears that type's override.
type workspaceRateLimitSetValue struct {
	Type  string `json:"type"`
	Value *int64 `json:"value"`
}

type workspaceRateLimitSetRequest struct {
	Limits []workspaceRateLimitSetValue `json:"limits"`
}

// --- Schema ---

func (r *WorkspaceRateLimitResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_workspace_rate_limit"
}

func (r *WorkspaceRateLimitResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Sets a workspace's rate-limit overrides for one rate-limit group via the Admin API. " +
			"The resource manages only the limiter types listed in `limits`; destroying it clears those overrides, " +
			"so the workspace falls back to the organization's limits. " +
			"Requires `admin_api_key` (or `ANTHROPIC_ADMIN_API_KEY`) to be configured on the provider.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Composite identifier in the form `<workspace_id>:<rate_limit_id>`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"workspace_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the workspace to set overrides for.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"rate_limit_id": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "The `id` of the organization's rate-limit entry for the group to override: " +
					"a `rate_limit_id` from `anthropic_workspace_rate_limits` with `include_inherited = true`.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"limits": schema.MapAttribute{
				Required:    true,
				ElementType: types.Int64Type,
				MarkdownDescription: "Override values keyed by limiter type: `" + strings.Join(workspaceRateLimitTypes, "`, `") + "`. " +
					"A value above the organization's own limit is stored, but the lower of the two is enforced. " +
					"Removing a key clears that override. Changes take effect within about an hour.",
				Validators: []validator.Map{
					mapvalidator.SizeAtLeast(1),
					mapvalidator.KeysAre(stringvalidator.OneOf(workspaceRateLimitTypes...)),
					mapvalidator.ValueInt64sAre(int64validator.Between(0, maxWorkspaceRateLimitValue)),
				},
			},
			"group": schema.SingleNestedAttribute{
				Computed:            true,
				MarkdownDescription: rateLimitGroupDescription,
				PlanModifiers:       []planmodifier.Object{objectplanmodifier.UseStateForUnknown()},
				Attributes: map[string]schema.Attribute{
					"id": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "Identifier of the rate-limit group (`rlg_...`), the same in every organization.",
					},
					"type": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "Group type: one of " + rateLimitGroupTypes + ".",
					},
					"display_name": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "Human-readable name of a model group, for display only. Null for other group types.",
					},
				},
			},
		},
	}
}

// --- Configure ---

func (r *WorkspaceRateLimitResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *WorkspaceRateLimitResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan WorkspaceRateLimitResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	planned, diags := rateLimitValues(ctx, plan.Limits)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	entry, err := r.setOverrides(ctx, plan.WorkspaceID.ValueString(), plan.RateLimitID.ValueString(), buildWorkspaceRateLimitPatch(planned, nil))
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to set workspace rate-limit overrides: %s", err))
		return
	}

	plan.ID = types.StringValue(workspaceRateLimitID(plan.WorkspaceID.ValueString(), plan.RateLimitID.ValueString()))
	plan.Group, diags = rateLimitGroupValue(entry.Group)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// limits stays as planned: the endpoint stores exactly the values sent.
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// --- Read ---

func (r *WorkspaceRateLimitResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state WorkspaceRateLimitResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Without include_inherited the list carries only stored overrides, so a
	// managed type cleared out of band is absent and shows up as drift.
	items, err := listWorkspaceRateLimits(ctx, r.adminClient, state.WorkspaceID.ValueString(), "", false)
	if err != nil {
		if admin.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list workspace rate limits: %s", err))
		return
	}

	var entry *workspaceRateLimitAPIItem
	for i := range items {
		if items[i].RateLimitID == state.RateLimitID.ValueString() {
			entry = &items[i]
			break
		}
	}

	importing := state.Limits.IsNull()
	if entry == nil && importing {
		// Nothing to import: the group has no override in this workspace (or
		// the rate_limit_id is wrong). Removing the resource makes
		// `terraform import` report a missing object instead of importing an
		// empty one.
		resp.State.RemoveResource(ctx)
		return
	}

	var managed map[string]int64
	if !importing {
		var diags diag.Diagnostics
		managed, diags = rateLimitValues(ctx, state.Limits)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	limits, diags := types.MapValueFrom(ctx, types.Int64Type, overridesToState(entry, managed, importing))
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	state.Limits = limits

	if entry != nil {
		state.Group, diags = rateLimitGroupValue(entry.Group)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
	}
	state.ID = types.StringValue(workspaceRateLimitID(state.WorkspaceID.ValueString(), state.RateLimitID.ValueString()))

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

// --- Update ---

func (r *WorkspaceRateLimitResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state WorkspaceRateLimitResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	planned, diags := rateLimitValues(ctx, plan.Limits)
	resp.Diagnostics.Append(diags...)
	prior, diags := rateLimitValues(ctx, state.Limits)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	entry, err := r.setOverrides(ctx, state.WorkspaceID.ValueString(), state.RateLimitID.ValueString(), buildWorkspaceRateLimitPatch(planned, prior))
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update workspace rate-limit overrides: %s", err))
		return
	}

	plan.ID = state.ID
	// group is planned from state (UseStateForUnknown); only a response that
	// carries one may replace it, so a trimmed response cannot null it out.
	if entry.Group != nil {
		plan.Group, diags = rateLimitGroupValue(entry.Group)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// --- Delete ---

func (r *WorkspaceRateLimitResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state WorkspaceRateLimitResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	prior, diags := rateLimitValues(ctx, state.Limits)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Every managed type is cleared; types the resource does not manage keep
	// their overrides. An empty map (all cleared out of band) needs no call,
	// and the endpoint rejects an empty limits list anyway.
	patch := buildWorkspaceRateLimitPatch(nil, prior)
	if len(patch) == 0 {
		return
	}

	_, err := r.setOverrides(ctx, state.WorkspaceID.ValueString(), state.RateLimitID.ValueString(), patch)
	if err != nil && !admin.IsNotFound(err) {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to clear workspace rate-limit overrides: %s", err))
	}
}

// --- ImportState ---

func (r *WorkspaceRateLimitResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	workspaceID, rateLimitID, ok := strings.Cut(req.ID, ":")
	if !ok || workspaceID == "" || rateLimitID == "" {
		resp.Diagnostics.AddError(
			"Invalid import ID",
			"Expected format: <workspace_id>:<rate_limit_id>",
		)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("workspace_id"), workspaceID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("rate_limit_id"), rateLimitID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

// ============================================================================
// Helper functions
// ============================================================================

func workspaceRateLimitID(workspaceID, rateLimitID string) string {
	return workspaceID + ":" + rateLimitID
}

// setOverrides POSTs one set-overrides request and returns the group's entry.
func (r *WorkspaceRateLimitResource) setOverrides(ctx context.Context, workspaceID, rateLimitID string, limits []workspaceRateLimitSetValue) (*workspaceRateLimitAPIItem, error) {
	apiPath := workspaceRateLimitsPath(workspaceID) + "/" + url.PathEscape(rateLimitID)
	body, err := r.adminClient.DoRequest(ctx, "POST", apiPath, workspaceRateLimitSetRequest{Limits: limits})
	if err != nil {
		return nil, err
	}
	var entry workspaceRateLimitAPIItem
	if err := json.Unmarshal(body, &entry); err != nil {
		return nil, fmt.Errorf("parse set workspace rate limits response: %w", err)
	}
	return &entry, nil
}

// rateLimitValues reads a limits map; null or unknown reads as empty.
func rateLimitValues(ctx context.Context, m types.Map) (map[string]int64, diag.Diagnostics) {
	values := map[string]int64{}
	if m.IsNull() || m.IsUnknown() {
		return values, nil
	}
	diags := m.ElementsAs(ctx, &values, false)
	return values, diags
}

// buildWorkspaceRateLimitPatch returns the limits to send: every planned type
// with its value, plus null for each type in prior that the plan no longer
// has (which clears that override). Sorted by type for a stable body.
func buildWorkspaceRateLimitPatch(planned, prior map[string]int64) []workspaceRateLimitSetValue {
	patch := make([]workspaceRateLimitSetValue, 0, len(planned)+len(prior))
	for t, v := range planned {
		patch = append(patch, workspaceRateLimitSetValue{Type: t, Value: &v})
	}
	for t := range prior {
		if _, kept := planned[t]; !kept {
			patch = append(patch, workspaceRateLimitSetValue{Type: t, Value: nil})
		}
	}
	slices.SortFunc(patch, func(a, b workspaceRateLimitSetValue) int { return strings.Compare(a.Type, b.Type) })
	return patch
}

// overridesToState returns the limits to store after a read: the stored
// workspace overrides of the group, restricted to the managed types unless
// importing (when every override is taken). A nil entry (no override left in
// the group) yields an empty map.
func overridesToState(entry *workspaceRateLimitAPIItem, managed map[string]int64, importing bool) map[string]int64 {
	out := map[string]int64{}
	if entry == nil {
		return out
	}
	for _, l := range entry.Limits {
		// Defensive: without include_inherited only overrides are listed.
		if l.Source != nil && l.Source.Type != "workspace" {
			continue
		}
		if _, ok := managed[l.Type]; ok || importing {
			out[l.Type] = l.Value
		}
	}
	return out
}
