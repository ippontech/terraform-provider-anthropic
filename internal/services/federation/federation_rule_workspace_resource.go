// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &FederationRuleWorkspaceResource{}
var _ resource.ResourceWithImportState = &FederationRuleWorkspaceResource{}

func NewFederationRuleWorkspaceResource() resource.Resource {
	return &FederationRuleWorkspaceResource{}
}

// FederationRuleWorkspaceResource defines the resource implementation.
type FederationRuleWorkspaceResource struct {
	client *providerdata.OAuthClient
}

// FederationRuleWorkspaceResourceModel describes the resource data model.
type FederationRuleWorkspaceResourceModel struct {
	ID               types.String `tfsdk:"id"`
	FederationRuleID types.String `tfsdk:"federation_rule_id"`
	WorkspaceID      types.String `tfsdk:"workspace_id"`
	WorkspaceName    types.String `tfsdk:"workspace_name"`
	CreatedAt        types.String `tfsdk:"created_at"`
	CreatedByActorID types.String `tfsdk:"created_by_actor_id"`
}

// --- Schema ---

func (r *FederationRuleWorkspaceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_federation_rule_workspace"
}

func (r *FederationRuleWorkspaceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Enables a Workload Identity Federation (WIF) rule for an additional workspace (beta). A federation " +
			"rule's create-time `workspace_id` already enables it for one workspace — this resource manages the extra ones a rule " +
			"should also be usable from; do not also manage that first workspace with this resource, or Terraform and the API will " +
			"fight over the same enablement.\n\n" +
			"There is no update endpoint: every attribute is immutable and any change forces replacement. Destroying this resource " +
			"disables the rule for the workspace (hard remove; the operation is idempotent, so it also succeeds if the enablement was " +
			"already removed out-of-band).\n\n" +
			"Requires an org:admin OAuth bearer token (`auth_token` / `ANTHROPIC_AUTH_TOKEN`); Admin API keys are not accepted on this " +
			"endpoint.",
		Attributes: map[string]schema.Attribute{
			// --- Required ---
			"federation_rule_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Tagged ID (`fdrl_...`) of the federation rule to enable for the workspace. Immutable.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"workspace_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Tagged ID (`wrkspc_...`) of the workspace to enable the rule for. Immutable.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},

			// --- Computed ---
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Composite identifier in the form `<federation_rule_id>:<workspace_id>`.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"workspace_name": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Workspace display name at read time.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when this workspace was enabled for the rule.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"created_by_actor_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Tagged ID (`user_...`/`svac_...`) of the actor that enabled this workspace for the rule, if known.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

// --- Configure ---

func (r *FederationRuleWorkspaceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
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

func (r *FederationRuleWorkspaceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data FederationRuleWorkspaceResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	federationRuleID := data.FederationRuleID.ValueString()
	workspaceID := data.WorkspaceID.ValueString()

	added, err := r.client.Beta.Organization.Federation.Rules.Workspaces.Add(ctx, federationRuleID, anthropic.BetaOrganizationFederationRuleWorkspaceAddParams{
		WorkspaceID: workspaceID,
	})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to enable federation rule for workspace: %s", err))
		return
	}

	// The enable response's workspace_name is always null — the API only
	// populates it when listing. Look the entry up right away so state is
	// fully populated after the first apply instead of only after the next
	// refresh. Best-effort: the enablement itself already succeeded, so a
	// failed or empty lookup here falls back to the Add response rather than
	// failing the apply.
	if found, lookupErr := findFederationRuleWorkspace(ctx, r.client.Client, federationRuleID, workspaceID); lookupErr == nil && found != nil {
		added = found
	}

	mapFederationRuleWorkspaceToState(added, &data)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Read ---

func (r *FederationRuleWorkspaceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data FederationRuleWorkspaceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	found, err := findFederationRuleWorkspace(ctx, r.client.Client, data.FederationRuleID.ValueString(), data.WorkspaceID.ValueString())
	if err != nil {
		var apiErr *anthropic.Error
		if errors.As(err, &apiErr) && apiErr.StatusCode == 404 {
			// The federation rule itself was archived/deleted out-of-band.
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list federation rule workspaces: %s", err))
		return
	}
	if found == nil {
		// The enablement was removed out-of-band (e.g. via the Console).
		resp.State.RemoveResource(ctx)
		return
	}

	mapFederationRuleWorkspaceToState(found, &data)

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Update ---

func (r *FederationRuleWorkspaceResource) Update(_ context.Context, _ resource.UpdateRequest, _ *resource.UpdateResponse) {
	// Never called: every attribute has a RequiresReplace plan modifier, and
	// there is no update endpoint to call even out-of-band.
}

// --- Delete ---

func (r *FederationRuleWorkspaceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data FederationRuleWorkspaceResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Remove's signature is inverted relative to Add/List: the workspace ID
	// travels positionally and the federation rule ID travels in the params
	// struct. Getting this backwards compiles (both are plain strings) and
	// only fails at request time against the wrong URL path.
	_, err := r.client.Beta.Organization.Federation.Rules.Workspaces.Remove(ctx, data.WorkspaceID.ValueString(), anthropic.BetaOrganizationFederationRuleWorkspaceRemoveParams{
		FederationRuleID: data.FederationRuleID.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to disable federation rule for workspace: %s", err))
	}
}

// --- ImportState ---

func (r *FederationRuleWorkspaceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	parts := strings.SplitN(req.ID, ":", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		resp.Diagnostics.AddError(
			"Invalid import ID",
			"Expected format: <federation_rule_id>:<workspace_id>",
		)
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("federation_rule_id"), parts[0])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("workspace_id"), parts[1])...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)
}

// ============================================================================
// Helper functions
// ============================================================================

// findFederationRuleWorkspace pages through the rule's enabled workspaces
// looking for workspaceID. It returns (nil, nil) when pagination completes
// without a match (the enablement is gone, or was never there), and a non-nil
// error when the rule itself is gone (surfaces as a 404 from the underlying
// List call) or the request otherwise failed.
func findFederationRuleWorkspace(ctx context.Context, client *anthropic.Client, federationRuleID, workspaceID string) (*anthropic.BetaFederationRuleWorkspace, error) {
	pager := client.Beta.Organization.Federation.Rules.Workspaces.ListAutoPaging(ctx, federationRuleID, anthropic.BetaOrganizationFederationRuleWorkspaceListParams{})
	for pager.Next() {
		w := pager.Current()
		if w.WorkspaceID == workspaceID {
			return &w, nil
		}
	}
	if err := pager.Err(); err != nil {
		return nil, err
	}
	return nil, nil
}

// mapFederationRuleWorkspaceToState maps the API response to the Terraform
// state model.
func mapFederationRuleWorkspaceToState(w *anthropic.BetaFederationRuleWorkspace, data *FederationRuleWorkspaceResourceModel) {
	data.ID = types.StringValue(w.FederationRuleID + ":" + w.WorkspaceID)
	data.FederationRuleID = types.StringValue(w.FederationRuleID)
	data.WorkspaceID = types.StringValue(w.WorkspaceID)
	data.WorkspaceName = stringOrNull(w.WorkspaceName)
	data.CreatedByActorID = stringOrNull(w.CreatedByActorID)

	if w.CreatedAt.IsZero() {
		data.CreatedAt = types.StringNull()
	} else {
		data.CreatedAt = types.StringValue(w.CreatedAt.Format(time.RFC3339))
	}
}

// stringOrNull maps an API "" (Go zero value for a required-but-optional
// string field) to a null Terraform value.
func stringOrNull(s string) types.String {
	if s == "" {
		return types.StringNull()
	}
	return types.StringValue(s)
}
