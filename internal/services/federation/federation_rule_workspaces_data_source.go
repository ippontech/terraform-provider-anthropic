// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation

import (
	"context"
	"fmt"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &FederationRuleWorkspacesDataSource{}

func NewFederationRuleWorkspacesDataSource() datasource.DataSource {
	return &FederationRuleWorkspacesDataSource{}
}

// FederationRuleWorkspacesDataSource defines the data source implementation.
//
// This endpoint (like the other Workload Identity Federation admin endpoints)
// rejects API keys outright and requires an org:admin OAuth bearer token, so it
// is configured from pd.OAuthClient rather than the standard pd.Client.
type FederationRuleWorkspacesDataSource struct {
	client *providerdata.OAuthClient
}

// FederationRuleWorkspacesDataSourceModel describes the data source data model.
type FederationRuleWorkspacesDataSourceModel struct {
	FederationRuleID types.String `tfsdk:"federation_rule_id"`
	Workspaces       types.List   `tfsdk:"workspaces"`
}

// federationRuleWorkspacesListItemAttrTypes describes the attribute types of
// each element in the "workspaces" list. Named with the "List" suffix (as
// opposed to a bare federationRuleWorkspaceAttrTypes) so it cannot collide with
// the analogous helper the sibling federation_rule_workspace resource (enable
// /disable, singular) is expected to define in this same package.
var federationRuleWorkspacesListItemAttrTypes = map[string]attr.Type{
	"workspace_id":        types.StringType,
	"workspace_name":      types.StringType,
	"created_at":          types.StringType,
	"created_by_actor_id": types.StringType,
}

func (d *FederationRuleWorkspacesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_federation_rule_workspaces"
}

func (d *FederationRuleWorkspacesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists the workspaces where a Workload Identity Federation rule (beta) is enabled. " +
			"All results are returned in a single response by the API, so no pagination is required. " +
			"Only explicit per-workspace enablements are returned; for rules with `applies_to_all_workspaces` or a " +
			"legacy single `workspace_id`, check those fields on the `anthropic_federation_rule` data source instead. " +
			"Requires an org:admin OAuth bearer token (`auth_token` / `ANTHROPIC_AUTH_TOKEN`); Admin API keys are not accepted.",
		Attributes: map[string]schema.Attribute{
			// --- Required ---
			"federation_rule_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the federation rule whose enabled workspaces to list.",
			},

			// --- Computed output ---
			"workspaces": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "List of workspaces where the federation rule is enabled.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"workspace_id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Tagged ID of the workspace this rule is enabled for.",
						},
						"workspace_name": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Display name of the workspace.",
						},
						"created_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "RFC 3339 timestamp of when this workspace was enabled for the rule.",
						},
						"created_by_actor_id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Tagged ID (`user_...` or `svac_...`) of the actor that enabled this workspace for the rule, if known.",
						},
					},
				},
			},
		},
	}
}

func (d *FederationRuleWorkspacesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	pd, ok := req.ProviderData.(*providerdata.ProviderData)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *providerdata.ProviderData, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}

	if !providerrors.RequireOAuthDataSourceClient(pd.OAuthClient, &resp.Diagnostics) {
		return
	}

	d.client = pd.OAuthClient
}

func (d *FederationRuleWorkspacesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data FederationRuleWorkspacesDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	federationRuleID := data.FederationRuleID.ValueString()

	// No anthropic-beta header is sent: the live API for this endpoint only
	// requires anthropic-version, matching the other WIF admin endpoints (#137).
	pager := d.client.Beta.Organization.Federation.Rules.Workspaces.ListAutoPaging(
		ctx, federationRuleID, anthropic.BetaOrganizationFederationRuleWorkspaceListParams{},
	)

	workspaceObjs := make([]attr.Value, 0)
	for pager.Next() {
		w := pager.Current()
		obj, diags := mapFederationRuleWorkspacesListItem(&w)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		workspaceObjs = append(workspaceObjs, obj)
	}

	if err := pager.Err(); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list workspaces for federation rule %q: %s", federationRuleID, err))
		return
	}

	workspacesList, diags := types.ListValue(types.ObjectType{AttrTypes: federationRuleWorkspacesListItemAttrTypes}, workspaceObjs)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.Workspaces = workspacesList

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// mapFederationRuleWorkspacesListItem converts an API federation-rule-workspace
// enablement into a Terraform object value for inclusion in the "workspaces"
// list.
func mapFederationRuleWorkspacesListItem(w *anthropic.BetaFederationRuleWorkspace) (attr.Value, diag.Diagnostics) {
	var diags diag.Diagnostics

	createdByActorID := types.StringNull()
	if w.CreatedByActorID != "" {
		createdByActorID = types.StringValue(w.CreatedByActorID)
	}

	obj, d := types.ObjectValue(federationRuleWorkspacesListItemAttrTypes, map[string]attr.Value{
		"workspace_id":        types.StringValue(w.WorkspaceID),
		"workspace_name":      types.StringValue(w.WorkspaceName),
		"created_at":          types.StringValue(w.CreatedAt.Format(time.RFC3339)),
		"created_by_actor_id": createdByActorID,
	})
	diags.Append(d...)
	return obj, diags
}
