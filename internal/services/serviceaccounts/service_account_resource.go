// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package serviceaccounts

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &ServiceAccountResource{}
var _ resource.ResourceWithImportState = &ServiceAccountResource{}

// serviceAccountNameRegexp restricts names to the slug shape the API expects:
// lowercase letters, digits and hyphens only.
var serviceAccountNameRegexp = regexp.MustCompile(`^[a-z0-9-]+$`)

// The production bounds of awaitServiceAccountUpdateVisible's wait. They are
// consts, and the function takes them as arguments, so the unit tests can
// shrink the loop to milliseconds without mutating shared state — see the
// read-after-write note on the vaults API (vault_resource.go), which shares
// the same underlying Beta managed-agents infrastructure and has not been
// ruled out for this endpoint.
const (
	serviceAccountConsistencyTimeout  = 5 * time.Second
	serviceAccountConsistencyInterval = 200 * time.Millisecond
)

func NewServiceAccountResource() resource.Resource {
	return &ServiceAccountResource{}
}

// ServiceAccountResource defines the resource implementation.
type ServiceAccountResource struct {
	client *providerdata.OAuthClient
}

// ServiceAccountResourceModel describes the resource data model.
type ServiceAccountResourceModel struct {
	ID                types.String `tfsdk:"id"`
	Name              types.String `tfsdk:"name"`
	Description       types.String `tfsdk:"description"`
	OrganizationRole  types.String `tfsdk:"organization_role"`
	CreatedAt         types.String `tfsdk:"created_at"`
	UpdatedAt         types.String `tfsdk:"updated_at"`
	ArchivedAt        types.String `tfsdk:"archived_at"`
	CreatedByActorID  types.String `tfsdk:"created_by_actor_id"`
	UpdatedByActorID  types.String `tfsdk:"updated_by_actor_id"`
	ArchivedByActorID types.String `tfsdk:"archived_by_actor_id"`
}

// --- Schema ---

func (r *ServiceAccountResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_service_account"
}

func (r *ServiceAccountResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a Workload Identity Federation (WIF) service account: a named, non-human identity that federation " +
			"rules target (beta). A service account is a pure identity — name plus organization — and carries no authorization of its " +
			"own; authorization lives on whatever federation rule references it.\n\n" +
			"There is no hard-delete endpoint for service accounts: destroying this resource always archives it. Archiving is rejected " +
			"with an HTTP 400 while a live (non-archived) federation rule still targets the service account, so destroy any " +
			"`anthropic_federation_rule` resources that reference this service account first — Terraform's dependency graph handles the " +
			"ordering automatically when the rule references this resource's `id`.\n\n" +
			"Requires an org:admin OAuth bearer token (`auth_token` / `ANTHROPIC_AUTH_TOKEN`); Admin API keys are not accepted on this " +
			"endpoint.",
		Attributes: map[string]schema.Attribute{
			// --- Required ---
			"name": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Admin-chosen slug identifier, unique within the organization (a duplicate name returns a 409). " +
					"Lowercase letters, digits and hyphens only, 1–255 characters. Immutable — changing this forces replacement, since " +
					"the update API has no `name` field.",
				Validators: []validator.String{
					stringvalidator.LengthBetween(1, 255),
					stringvalidator.RegexMatches(serviceAccountNameRegexp, "must contain only lowercase letters, digits, and hyphens"),
				},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},

			// --- Optional ---
			"description": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Optional free-text description.",
			},
			"organization_role": schema.StringAttribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "Org-level role: `developer` or `admin`. Defaults to `developer`. A federation rule may only be " +
					"created or retargeted to grant `org:admin` scope when this is `admin`. Setting this to `admin` (even when unchanged) " +
					"requires an interactive credential (a user OAuth token or a Console session) — a workload cannot create or promote " +
					"`admin`-role service accounts.",
				Validators:    []validator.String{stringvalidator.OneOf("developer", "admin")},
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},

			// --- Computed ---
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Unique service account identifier assigned by the API (`svac_...`).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the service account was created.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the service account was last updated.",
			},
			"archived_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when the service account was archived, or null while it is live.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"created_by_actor_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Tagged ID (`user_...`/`svac_...`) of the actor that created this service account.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"updated_by_actor_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Tagged ID (`user_...`/`svac_...`) of the actor that last updated this service account.",
			},
			"archived_by_actor_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Tagged ID (`user_...`/`svac_...`) of the actor that archived this service account, or null while it is live.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

// --- Configure ---

func (r *ServiceAccountResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

	if !providerrors.RequireOAuthResourceClient(pd.OAuthClient, &resp.Diagnostics) {
		return
	}

	r.client = pd.OAuthClient
}

// --- Create ---

func (r *ServiceAccountResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data ServiceAccountResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	params := buildServiceAccountCreateParams(&data)

	sa, err := r.client.Beta.Organization.ServiceAccounts.New(ctx, params)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create service account: %s", err))
		return
	}

	resp.Diagnostics.Append(mapServiceAccountToState(sa, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Read ---

func (r *ServiceAccountResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data ServiceAccountResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	sa, err := r.client.Beta.Organization.ServiceAccounts.Get(ctx, data.ID.ValueString(), anthropic.BetaOrganizationServiceAccountGetParams{})
	if err != nil {
		// The service account was deleted out-of-band: drop it from state so the
		// next plan recreates it instead of erroring forever.
		var apierr *anthropic.Error
		if errors.As(err, &apierr) && apierr.StatusCode == 404 {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read service account: %s", err))
		return
	}

	resp.Diagnostics.Append(mapServiceAccountToState(sa, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Update ---

// isTerminalServiceAccountReadError reports whether a Get failure is one the
// poll can never recover from: the service account is gone, or the credential
// no longer has access to it. Retrying those until the deadline would stall
// the apply for seconds on a read that will never converge.
func isTerminalServiceAccountReadError(err error) bool {
	var apierr *anthropic.Error
	if !errors.As(err, &apierr) {
		return false
	}

	switch apierr.StatusCode {
	case 401, 403, 404:
		return true
	default:
		return false
	}
}

// awaitServiceAccountUpdateVisible polls Get until the stored service account
// is at least as new as writtenAt, the updated_at returned by the write
// itself. Mirrors awaitVaultUpdateVisible (vault_resource.go): the vaults API
// serves stale reads for up to ~1s after a write, and this endpoint shares the
// same underlying Beta managed-agents infrastructure but has not itself been
// probed for the same staleness.
//
// It is deliberately best-effort: on a read error, a timeout, or a cancelled
// context it returns without reporting a diagnostic. The write has already
// succeeded, so failing the apply here would turn a cosmetic staleness window
// into a hard error; the worst case of giving up is the phantom diff we were
// trying to avoid.
func awaitServiceAccountUpdateVisible(ctx context.Context, client *providerdata.OAuthClient, id string, writtenAt time.Time, timeout, interval time.Duration) {
	deadline := time.Now().Add(timeout)

	for {
		sa, err := client.Beta.Organization.ServiceAccounts.Get(ctx, id, anthropic.BetaOrganizationServiceAccountGetParams{})
		switch {
		case err == nil:
			if !sa.UpdatedAt.Before(writtenAt) {
				return
			}
		case isTerminalServiceAccountReadError(err):
			tflog.Warn(ctx, "service account became unreadable while waiting for the update to be visible; giving up on the consistency wait", map[string]any{
				"service_account_id": id,
				"error":              err.Error(),
			})
			return
		}

		if time.Now().After(deadline) {
			tflog.Warn(ctx, "service account update not visible before the consistency timeout; the next plan may show a transient diff", map[string]any{
				"service_account_id": id,
				"timeout":            timeout.String(),
			})
			return
		}

		select {
		case <-ctx.Done():
			return
		case <-time.After(interval):
		}
	}
}

func (r *ServiceAccountResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan ServiceAccountResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var state ServiceAccountResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	params := buildServiceAccountUpdateParams(&plan, &state)

	sa, err := r.client.Beta.Organization.ServiceAccounts.Update(ctx, state.ID.ValueString(), params)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update service account: %s", err))
		return
	}

	resp.Diagnostics.Append(mapServiceAccountToState(sa, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	awaitServiceAccountUpdateVisible(ctx, r.client, state.ID.ValueString(), sa.UpdatedAt, serviceAccountConsistencyTimeout, serviceAccountConsistencyInterval)

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

// --- Delete (always archives; there is no hard-delete endpoint) ---

func (r *ServiceAccountResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data ServiceAccountResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.Beta.Organization.ServiceAccounts.Archive(ctx, data.ID.ValueString(), anthropic.BetaOrganizationServiceAccountArchiveParams{})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to archive service account: %s", err))
	}
}

// --- ImportState ---

func (r *ServiceAccountResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// ============================================================================
// Helper functions
// ============================================================================

// buildServiceAccountCreateParams converts the planned config into a create
// request. description and organization_role are both left at their zero value
// (omitted, via `omitzero`) when unset in config, so the API applies its
// defaults (no description, organization_role "developer").
func buildServiceAccountCreateParams(data *ServiceAccountResourceModel) anthropic.BetaOrganizationServiceAccountNewParams {
	params := anthropic.BetaOrganizationServiceAccountNewParams{
		Name: data.Name.ValueString(),
	}

	if !data.Description.IsNull() && !data.Description.IsUnknown() {
		params.Description = param.NewOpt(data.Description.ValueString())
	}

	if !data.OrganizationRole.IsNull() && !data.OrganizationRole.IsUnknown() {
		params.OrganizationRole = anthropic.BetaOrganizationServiceAccountNewParamsOrganizationRole(data.OrganizationRole.ValueString())
	}

	return params
}

// buildServiceAccountUpdateParams converts the planned config into an update
// request. Unlike create, description is always sent explicitly — as an
// explicit null via param.Null[string]() when cleared in config — because the
// update API treats an omitted description as "leave unchanged", and would
// otherwise never converge a removed description to the server's empty string.
//
// organization_role is only sent when it actually changes relative to prior
// state. organization_role is Optional+Computed with UseStateForUnknown(), so
// plan.OrganizationRole is always known once the service account exists —
// sending it unconditionally would resend "admin" on every update to an
// admin-role service account, which the API rejects from a non-interactive
// (WIF/OAuth bearer) credential even when the value is unchanged.
func buildServiceAccountUpdateParams(plan, state *ServiceAccountResourceModel) anthropic.BetaOrganizationServiceAccountUpdateParams {
	params := anthropic.BetaOrganizationServiceAccountUpdateParams{}

	if plan.Description.IsNull() {
		params.Description = param.Null[string]()
	} else if !plan.Description.IsUnknown() {
		params.Description = param.NewOpt(plan.Description.ValueString())
	}

	if !plan.OrganizationRole.IsNull() && !plan.OrganizationRole.IsUnknown() &&
		plan.OrganizationRole.ValueString() != state.OrganizationRole.ValueString() {
		params.OrganizationRole = anthropic.BetaOrganizationServiceAccountUpdateParamsOrganizationRole(plan.OrganizationRole.ValueString())
	}

	return params
}

// mapServiceAccountToState maps the API response to the Terraform state model.
func mapServiceAccountToState(sa *anthropic.BetaServiceAccount, data *ServiceAccountResourceModel) diag.Diagnostics {
	var diags diag.Diagnostics

	data.ID = types.StringValue(sa.ID)
	data.Name = types.StringValue(sa.Name)
	data.OrganizationRole = types.StringValue(string(sa.OrganizationRole))
	data.CreatedAt = types.StringValue(sa.CreatedAt.Format(time.RFC3339))
	data.UpdatedAt = types.StringValue(sa.UpdatedAt.Format(time.RFC3339))

	data.Description = descriptionOrNull(sa.Description, data.Description)
	data.CreatedByActorID = stringOrNull(sa.CreatedByActorID)
	data.UpdatedByActorID = stringOrNull(sa.UpdatedByActorID)
	data.ArchivedByActorID = stringOrNull(sa.ArchivedByActorID)

	if sa.ArchivedAt.IsZero() {
		data.ArchivedAt = types.StringNull()
	} else {
		data.ArchivedAt = types.StringValue(sa.ArchivedAt.Format(time.RFC3339))
	}

	return diags
}

// stringOrNull maps an API "" (Go zero value for a required-but-optional
// string field) to a null Terraform value, so an unset description or actor ID
// is represented as null rather than an empty string.
func stringOrNull(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}

// descriptionOrNull maps the API's description the same way stringOrNull
// does, with one exception: description is Optional (not Computed), so
// Terraform requires the final state to echo back a practitioner-configured
// empty string rather than collapse it to null. planned is data.Description
// as already populated from Plan (Create/Update) or State (Read) before this
// call; a known non-null value there means the practitioner explicitly
// configured `description = ""`, which must round-trip as "" instead of null.
func descriptionOrNull(apiValue string, planned types.String) types.String {
	if apiValue == "" {
		if !planned.IsNull() && !planned.IsUnknown() {
			return types.StringValue("")
		}
		return types.StringNull()
	}
	return types.StringValue(apiValue)
}
