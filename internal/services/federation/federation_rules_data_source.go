// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation

import (
	"context"
	"fmt"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &FederationRulesDataSource{}

func NewFederationRulesDataSource() datasource.DataSource {
	return &FederationRulesDataSource{}
}

// FederationRulesDataSource defines the data source implementation.
type FederationRulesDataSource struct {
	client *providerdata.OAuthClient
}

// FederationRulesDataSourceModel describes the data source data model.
type FederationRulesDataSourceModel struct {
	IssuerID        types.String `tfsdk:"issuer_id"`
	IncludeArchived types.Bool   `tfsdk:"include_archived"`
	Rules           types.List   `tfsdk:"rules"`
}

// --- attr.Type maps ---

var federationRulesMatchAttrTypes = map[string]attr.Type{
	"audience":       types.StringType,
	"claims":         types.MapType{ElemType: types.StringType},
	"condition":      types.StringType,
	"subject_prefix": types.StringType,
}

var federationRulesTargetAttrTypes = map[string]attr.Type{
	"type":                 types.StringType,
	"service_account_id":   types.StringType,
	"service_account_name": types.StringType,
}

var federationRulesItemAttrTypes = map[string]attr.Type{
	"id":                        types.StringType,
	"applies_to_all_workspaces": types.BoolType,
	"archived_at":               types.StringType,
	"archived_by_actor_id":      types.StringType,
	"attributes":                types.MapType{ElemType: types.StringType},
	"created_at":                types.StringType,
	"created_by_actor_id":       types.StringType,
	"description":               types.StringType,
	"issuer_id":                 types.StringType,
	"issuer_name":               types.StringType,
	"match":                     types.ObjectType{AttrTypes: federationRulesMatchAttrTypes},
	"name":                      types.StringType,
	"oauth_scope":               types.StringType,
	"target":                    types.ObjectType{AttrTypes: federationRulesTargetAttrTypes},
	"token_lifetime_seconds":    types.Int64Type,
	"type":                      types.StringType,
	"updated_at":                types.StringType,
	"updated_by_actor_id":       types.StringType,
	"workspace_id":              types.StringType,
	"workspace_ids":             types.ListType{ElemType: types.StringType},
}

// --- Metadata ---

func (d *FederationRulesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_federation_rules"
}

// --- Schema ---

func (d *FederationRulesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists Workload Identity Federation (WIF) rules in the organization (beta). All pages are fetched " +
			"automatically. Optionally filter by issuer with `issuer_id`. Archived rules are excluded unless `include_archived` is " +
			"`true`.\n\n" +
			"Requires an org:admin OAuth bearer token (`auth_token` / `ANTHROPIC_AUTH_TOKEN`); Admin API keys are not accepted on " +
			"this endpoint.",
		Attributes: map[string]schema.Attribute{
			"issuer_id": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Filter results to rules referencing this federation issuer (`fdis_...`).",
			},
			"include_archived": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "When true, archived rules are included in the results. Defaults to false.",
			},
			"rules": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "List of federation rules matching the given filters.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Tagged ID of the federation rule (`fdrl_...`).",
						},
						"applies_to_all_workspaces": schema.BoolAttribute{
							Computed: true,
							MarkdownDescription: "When true, this rule is enabled for every workspace in the org (including ones " +
								"created after the rule). `workspace_ids` is ignored at exchange time.",
						},
						"archived_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "RFC 3339 timestamp of when this rule was archived, or null while it is live.",
						},
						"archived_by_actor_id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Tagged ID (`user_...`/`svac_...`) of the actor that archived this rule, or null while it is live.",
						},
						"attributes": schema.MapAttribute{
							Computed:            true,
							ElementType:         types.StringType,
							MarkdownDescription: "CEL expressions extracting named values from claims. Not yet supported; always null.",
						},
						"created_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "RFC 3339 timestamp of when this rule was created.",
						},
						"created_by_actor_id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Tagged ID (`user_...`/`svac_...`) of the actor that created this rule.",
						},
						"description": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Optional free-text description.",
						},
						"issuer_id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Tagged ID of the issuer whose tokens this rule accepts.",
						},
						"issuer_name": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Issuer's display name at read time.",
						},
						"match": schema.SingleNestedAttribute{
							Computed:            true,
							MarkdownDescription: "Conditions the verified JWT must satisfy for this rule to apply.",
							Attributes: map[string]schema.Attribute{
								"audience": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "Exact match against the `aud` claim, or null when not set.",
								},
								"claims": schema.MapAttribute{
									Computed:            true,
									ElementType:         types.StringType,
									MarkdownDescription: "Exact-match `{claim: value}` pairs against top-level claims, or null when not set.",
								},
								"condition": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "CEL expression over claims, or null when not set.",
								},
								"subject_prefix": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "Match (or prefix-match, if ending with `*`) against the verified JWT `sub` claim, or null when not set.",
								},
							},
						},
						"name": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Admin-chosen slug identifier.",
						},
						"oauth_scope": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Space-separated OAuth scopes granted on the minted token.",
						},
						"target": schema.SingleNestedAttribute{
							Computed:            true,
							MarkdownDescription: "Identity that tokens minted via this rule act as.",
							Attributes: map[string]schema.Attribute{
								"type": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "Target type. Currently always `service_account`.",
								},
								"service_account_id": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "Tagged ID of the service account to mint tokens for.",
								},
								"service_account_name": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "Service account's display name at read time, or null when unavailable.",
								},
							},
						},
						"token_lifetime_seconds": schema.Int64Attribute{
							Computed: true,
							MarkdownDescription: "Lifetime in seconds of access tokens minted via this rule. Minted tokens are capped at " +
								"`max(60, min(this value, 2 x remaining assertion validity))` seconds.",
						},
						"type": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Object type. Always `federation_rule`.",
						},
						"updated_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "RFC 3339 timestamp of when this rule was last updated.",
						},
						"updated_by_actor_id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Tagged ID (`user_...`/`svac_...`) of the actor that last updated this rule.",
						},
						"workspace_id": schema.StringAttribute{
							Computed: true,
							MarkdownDescription: "Legacy single-workspace binding, or null when unset. Prefer `workspace_ids` and the " +
								"`/federation_rules/{federation_rule_id}/workspaces` sub-resource for managing workspace enablement.",
						},
						"workspace_ids": schema.ListAttribute{
							Computed:    true,
							ElementType: types.StringType,
							MarkdownDescription: "Tagged IDs of the workspaces this rule is enabled for. May be empty for older rules " +
								"that only carry the legacy `workspace_id` binding.",
						},
					},
				},
			},
		},
	}
}

// --- Configure ---

func (d *FederationRulesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

// --- Read ---

func (d *FederationRulesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data FederationRulesDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	listParams := anthropic.BetaOrganizationFederationRuleListParams{}
	if !data.IssuerID.IsNull() && !data.IssuerID.IsUnknown() {
		listParams.IssuerID = param.NewOpt(data.IssuerID.ValueString())
	}
	if !data.IncludeArchived.IsNull() && !data.IncludeArchived.IsUnknown() {
		listParams.IncludeArchived = param.NewOpt(data.IncludeArchived.ValueBool())
	}

	pager := d.client.Beta.Organization.Federation.Rules.ListAutoPaging(ctx, listParams)

	ruleObjs := make([]attr.Value, 0)
	for pager.Next() {
		rule := pager.Current()

		obj, diags := mapFederationRulesListEntry(ctx, rule)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}

		ruleObjs = append(ruleObjs, obj)
	}

	if err := pager.Err(); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list federation rules: %s", err))
		return
	}

	rulesList, diags := types.ListValue(types.ObjectType{AttrTypes: federationRulesItemAttrTypes}, ruleObjs)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	data.Rules = rulesList
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// mapFederationRulesListEntry converts a single BetaFederationRule into a
// Terraform object value matching federationRulesItemAttrTypes.
//
// Named uniquely to this data source (rather than a generic
// mapFederationRuleToState) so that sibling branches in the same package
// (federation_rule, federation_rule_workspace, ...) can define their own
// mapping helpers without a symbol collision once every WIF PR lands on main.
func mapFederationRulesListEntry(ctx context.Context, rule anthropic.BetaFederationRule) (attr.Value, diag.Diagnostics) {
	var diags diag.Diagnostics

	archivedAt := types.StringNull()
	if !rule.ArchivedAt.IsZero() {
		archivedAt = types.StringValue(rule.ArchivedAt.Format(time.RFC3339Nano))
	}

	archivedByActorID := types.StringNull()
	if rule.ArchivedByActorID != "" {
		archivedByActorID = types.StringValue(rule.ArchivedByActorID)
	}

	description := types.StringNull()
	if rule.Description != "" {
		description = types.StringValue(rule.Description)
	}

	workspaceID := types.StringNull()
	if rule.WorkspaceID != "" {
		workspaceID = types.StringValue(rule.WorkspaceID)
	}

	attributesMap := types.MapNull(types.StringType)
	if len(rule.Attributes) > 0 {
		var d diag.Diagnostics
		attributesMap, d = types.MapValueFrom(ctx, types.StringType, rule.Attributes)
		diags.Append(d...)
	}

	workspaceIDs, d := types.ListValueFrom(ctx, types.StringType, rule.WorkspaceIDs)
	diags.Append(d...)

	matchObj, d := mapFederationRulesMatch(ctx, rule.Match)
	diags.Append(d...)

	targetObj, d := mapFederationRulesTarget(rule.Target)
	diags.Append(d...)

	obj, d := types.ObjectValue(federationRulesItemAttrTypes, map[string]attr.Value{
		"id":                        types.StringValue(rule.ID),
		"applies_to_all_workspaces": types.BoolValue(rule.AppliesToAllWorkspaces),
		"archived_at":               archivedAt,
		"archived_by_actor_id":      archivedByActorID,
		"attributes":                attributesMap,
		"created_at":                types.StringValue(rule.CreatedAt.Format(time.RFC3339Nano)),
		"created_by_actor_id":       types.StringValue(rule.CreatedByActorID),
		"description":               description,
		"issuer_id":                 types.StringValue(rule.IssuerID),
		"issuer_name":               types.StringValue(rule.IssuerName),
		"match":                     matchObj,
		"name":                      types.StringValue(rule.Name),
		"oauth_scope":               types.StringValue(rule.OAuthScope),
		"target":                    targetObj,
		"token_lifetime_seconds":    types.Int64Value(rule.TokenLifetimeSeconds),
		"type":                      types.StringValue(string(rule.Type)),
		"updated_at":                types.StringValue(rule.UpdatedAt.Format(time.RFC3339Nano)),
		"updated_by_actor_id":       types.StringValue(rule.UpdatedByActorID),
		"workspace_id":              workspaceID,
		"workspace_ids":             workspaceIDs,
	})
	diags.Append(d...)

	return obj, diags
}

// mapFederationRulesMatch converts a BetaFederationRuleMatch into a Terraform
// object value matching federationRulesMatchAttrTypes.
func mapFederationRulesMatch(ctx context.Context, match anthropic.BetaFederationRuleMatch) (attr.Value, diag.Diagnostics) {
	var diags diag.Diagnostics

	audience := types.StringNull()
	if match.Audience != "" {
		audience = types.StringValue(match.Audience)
	}

	condition := types.StringNull()
	if match.Condition != "" {
		condition = types.StringValue(match.Condition)
	}

	subjectPrefix := types.StringNull()
	if match.SubjectPrefix != "" {
		subjectPrefix = types.StringValue(match.SubjectPrefix)
	}

	claims := types.MapNull(types.StringType)
	if len(match.Claims) > 0 {
		var d diag.Diagnostics
		claims, d = types.MapValueFrom(ctx, types.StringType, match.Claims)
		diags.Append(d...)
	}

	obj, d := types.ObjectValue(federationRulesMatchAttrTypes, map[string]attr.Value{
		"audience":       audience,
		"claims":         claims,
		"condition":      condition,
		"subject_prefix": subjectPrefix,
	})
	diags.Append(d...)

	return obj, diags
}

// mapFederationRulesTarget converts a BetaServiceAccountTarget into a
// Terraform object value matching federationRulesTargetAttrTypes.
func mapFederationRulesTarget(target anthropic.BetaServiceAccountTarget) (attr.Value, diag.Diagnostics) {
	serviceAccountName := types.StringNull()
	if target.ServiceAccountName != "" {
		serviceAccountName = types.StringValue(target.ServiceAccountName)
	}

	return types.ObjectValue(federationRulesTargetAttrTypes, map[string]attr.Value{
		"type":                 types.StringValue(string(target.Type)),
		"service_account_id":   types.StringValue(target.ServiceAccountID),
		"service_account_name": serviceAccountName,
	})
}
