// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
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

// FederationRuleDataSourceModel describes the data source data model. The
// data source exposes exactly the resource's attributes (match and target
// nested objects included), so it shares the resource model and its
// mapFederationRuleToState mapping.
type FederationRuleDataSourceModel = FederationRuleResourceModel

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
				MarkdownDescription: "Tagged IDs of the workspaces this rule is enabled for. May be empty for older rules that only carry the legacy `workspace_id` binding.",
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

	resp.Diagnostics.Append(mapFederationRuleToState(ctx, rule, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
