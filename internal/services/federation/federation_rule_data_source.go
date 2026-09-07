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
var _ datasource.DataSource = &FederationRuleDataSource{}

func NewFederationRuleDataSource() datasource.DataSource {
	return &FederationRuleDataSource{}
}

// FederationRuleDataSource defines the data source implementation.
//
// This is a read-only Workload Identity Federation (WIF, #137) lookup. The
// federation rules endpoints reject API keys outright, so this data source
// requires the OAuth bearer client (pd.OAuthClient), not the standard or
// admin clients used elsewhere in the provider.
type FederationRuleDataSource struct {
	client *providerdata.OAuthClient
}

// federationRuleDataSourceMatchAttrTypes and federationRuleDataSourceTargetAttrTypes
// describe the "match" and "target" nested objects for types.ObjectValue /
// types.ObjectType construction.
//
// Names in this file are suffixed with "federationRuleDataSource" (or, for
// package-level helpers, prefixed with it) rather than reusing generic names
// like "mapFederationRuleToState". The federation_rule *resource* lives on a
// sibling, not-yet-merged branch and may define its own identically-purposed
// but differently-shaped helpers; keeping names unique here avoids a compile
// break when both land in the same package.
var federationRuleDataSourceMatchAttrTypes = map[string]attr.Type{
	"subject_prefix": types.StringType,
	"audience":       types.StringType,
	"claims":         types.MapType{ElemType: types.StringType},
	"condition":      types.StringType,
}

var federationRuleDataSourceTargetAttrTypes = map[string]attr.Type{
	"service_account_id":   types.StringType,
	"service_account_name": types.StringType,
}

// FederationRuleDataSourceModel describes the data source data model.
type FederationRuleDataSourceModel struct {
	ID                     types.String `tfsdk:"id"`
	Name                   types.String `tfsdk:"name"`
	Description            types.String `tfsdk:"description"`
	IssuerID               types.String `tfsdk:"issuer_id"`
	IssuerName             types.String `tfsdk:"issuer_name"`
	OAuthScope             types.String `tfsdk:"oauth_scope"`
	WorkspaceID            types.String `tfsdk:"workspace_id"`
	Match                  types.Object `tfsdk:"match"`
	Target                 types.Object `tfsdk:"target"`
	AppliesToAllWorkspaces types.Bool   `tfsdk:"applies_to_all_workspaces"`
	TokenLifetimeSeconds   types.Int64  `tfsdk:"token_lifetime_seconds"`
	Attributes             types.Map    `tfsdk:"attributes"`
	WorkspaceIDs           types.List   `tfsdk:"workspace_ids"`
	CreatedAt              types.String `tfsdk:"created_at"`
	CreatedByActorID       types.String `tfsdk:"created_by_actor_id"`
	UpdatedAt              types.String `tfsdk:"updated_at"`
	UpdatedByActorID       types.String `tfsdk:"updated_by_actor_id"`
	ArchivedAt             types.String `tfsdk:"archived_at"`
	ArchivedByActorID      types.String `tfsdk:"archived_by_actor_id"`
}

func (d *FederationRuleDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_federation_rule"
}

func (d *FederationRuleDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Fetches a single Workload Identity Federation rule by ID. " +
			"A federation rule binds an external OIDC identity (from a federation issuer) to an " +
			"Anthropic service account, optionally scoped to one or more workspaces. " +
			"Requires an org:admin OAuth bearer token (`auth_token` / `ANTHROPIC_AUTH_TOKEN`); Admin API keys are not accepted.",
		Attributes: map[string]schema.Attribute{
			// --- Required ---
			"id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Tagged ID of the federation rule (`fdrl_...`).",
			},

			// --- Computed ---
			"name": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Admin-chosen slug identifier.",
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
			"oauth_scope": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Space-separated OAuth scopes granted on the minted token.",
			},
			"workspace_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Legacy single-workspace binding. Prefer `workspace_ids`. Null when unset.",
			},
			"match": schema.SingleNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Conditions the verified JWT must satisfy for this rule to apply. All populated matcher fields must pass.",
				Attributes: map[string]schema.Attribute{
					"subject_prefix": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "Match against the verified JWT `sub` claim (exact, or prefix match if it ends with `*`). Null when unset.",
					},
					"audience": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "Exact match against the `aud` claim. Null when unset (the issuer's default audience applies).",
					},
					"claims": schema.MapAttribute{
						Computed:            true,
						ElementType:         types.StringType,
						MarkdownDescription: "Exact-match `{claim: value}` pairs against top-level claims. Null when unset.",
					},
					"condition": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "CEL expression over claims for logic the structural fields can't express. Null when unset.",
					},
				},
			},
			"target": schema.SingleNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Identity that tokens minted via this rule act as. Currently always a service account target.",
				Attributes: map[string]schema.Attribute{
					"service_account_id": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "Tagged ID of the service account to mint tokens for.",
					},
					"service_account_name": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "Service account's display name at read time. Null when unavailable.",
					},
				},
			},
			"applies_to_all_workspaces": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "When true, this rule is enabled for every workspace in the org (including ones created after the rule). `workspace_ids` is ignored at exchange time.",
			},
			"token_lifetime_seconds": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "Lifetime in seconds of access tokens minted via this rule.",
			},
			"attributes": schema.MapAttribute{
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "CEL expressions extracting named values from claims. Not yet supported by the API; always null.",
			},
			"workspace_ids": schema.ListAttribute{
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Tagged IDs of the workspaces this rule is enabled for. Null when empty.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when this rule was created.",
			},
			"created_by_actor_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Tagged ID (`user_`/`svac_`) of the actor that created this rule.",
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when this rule was last updated.",
			},
			"updated_by_actor_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Tagged ID (`user_`/`svac_`) of the actor that last updated this rule.",
			},
			"archived_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "RFC 3339 timestamp of when this rule was archived. Null when not archived.",
			},
			"archived_by_actor_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Tagged ID (`user_`/`svac_`) of the actor that archived this rule. Null when not archived.",
			},
		},
	}
}

func (d *FederationRuleDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *FederationRuleDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data FederationRuleDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	rule, err := d.client.Beta.Organization.Federation.Rules.Get(ctx, data.ID.ValueString(), anthropic.BetaOrganizationFederationRuleGetParams{})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to retrieve federation rule: %s", err))
		return
	}

	state, diags := mapFederationRuleDataSourceToState(rule)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, state)...)
}

// mapFederationRuleDataSourceToState converts an SDK BetaFederationRule into
// the Terraform state model. Kept as a standalone function (not a method) so
// unit tests can exercise it directly without a live client.
func mapFederationRuleDataSourceToState(rule *anthropic.BetaFederationRule) (*FederationRuleDataSourceModel, diag.Diagnostics) {
	var diags diag.Diagnostics
	data := &FederationRuleDataSourceModel{
		ID:                     types.StringValue(rule.ID),
		Name:                   types.StringValue(rule.Name),
		IssuerID:               types.StringValue(rule.IssuerID),
		IssuerName:             types.StringValue(rule.IssuerName),
		OAuthScope:             types.StringValue(rule.OAuthScope),
		AppliesToAllWorkspaces: types.BoolValue(rule.AppliesToAllWorkspaces),
		TokenLifetimeSeconds:   types.Int64Value(rule.TokenLifetimeSeconds),
		CreatedAt:              types.StringValue(rule.CreatedAt.Format(time.RFC3339Nano)),
		CreatedByActorID:       types.StringValue(rule.CreatedByActorID),
		UpdatedAt:              types.StringValue(rule.UpdatedAt.Format(time.RFC3339Nano)),
		UpdatedByActorID:       types.StringValue(rule.UpdatedByActorID),
	}

	if rule.Description != "" {
		data.Description = types.StringValue(rule.Description)
	} else {
		data.Description = types.StringNull()
	}

	if rule.WorkspaceID != "" {
		data.WorkspaceID = types.StringValue(rule.WorkspaceID)
	} else {
		data.WorkspaceID = types.StringNull()
	}

	if rule.ArchivedAt.IsZero() {
		data.ArchivedAt = types.StringNull()
	} else {
		data.ArchivedAt = types.StringValue(rule.ArchivedAt.Format(time.RFC3339Nano))
	}

	if rule.ArchivedByActorID != "" {
		data.ArchivedByActorID = types.StringValue(rule.ArchivedByActorID)
	} else {
		data.ArchivedByActorID = types.StringNull()
	}

	// match
	var subjectPrefix, audience, condition types.String
	if rule.Match.SubjectPrefix != "" {
		subjectPrefix = types.StringValue(rule.Match.SubjectPrefix)
	} else {
		subjectPrefix = types.StringNull()
	}
	if rule.Match.Audience != "" {
		audience = types.StringValue(rule.Match.Audience)
	} else {
		audience = types.StringNull()
	}
	if rule.Match.Condition != "" {
		condition = types.StringValue(rule.Match.Condition)
	} else {
		condition = types.StringNull()
	}

	var claims types.Map
	if len(rule.Match.Claims) > 0 {
		elements := make(map[string]attr.Value, len(rule.Match.Claims))
		for k, v := range rule.Match.Claims {
			elements[k] = types.StringValue(v)
		}
		m, d := types.MapValue(types.StringType, elements)
		diags.Append(d...)
		claims = m
	} else {
		claims = types.MapNull(types.StringType)
	}

	match, d := types.ObjectValue(federationRuleDataSourceMatchAttrTypes, map[string]attr.Value{
		"subject_prefix": subjectPrefix,
		"audience":       audience,
		"claims":         claims,
		"condition":      condition,
	})
	diags.Append(d...)
	data.Match = match

	// target
	var serviceAccountName types.String
	if rule.Target.ServiceAccountName != "" {
		serviceAccountName = types.StringValue(rule.Target.ServiceAccountName)
	} else {
		serviceAccountName = types.StringNull()
	}

	target, d := types.ObjectValue(federationRuleDataSourceTargetAttrTypes, map[string]attr.Value{
		"service_account_id":   types.StringValue(rule.Target.ServiceAccountID),
		"service_account_name": serviceAccountName,
	})
	diags.Append(d...)
	data.Target = target

	// attributes
	if len(rule.Attributes) > 0 {
		elements := make(map[string]attr.Value, len(rule.Attributes))
		for k, v := range rule.Attributes {
			elements[k] = types.StringValue(v)
		}
		m, d := types.MapValue(types.StringType, elements)
		diags.Append(d...)
		data.Attributes = m
	} else {
		data.Attributes = types.MapNull(types.StringType)
	}

	// workspace_ids
	if len(rule.WorkspaceIDs) > 0 {
		elements := make([]attr.Value, len(rule.WorkspaceIDs))
		for i, id := range rule.WorkspaceIDs {
			elements[i] = types.StringValue(id)
		}
		l, d := types.ListValue(types.StringType, elements)
		diags.Append(d...)
		data.WorkspaceIDs = l
	} else {
		data.WorkspaceIDs = types.ListNull(types.StringType)
	}

	return data, diags
}
