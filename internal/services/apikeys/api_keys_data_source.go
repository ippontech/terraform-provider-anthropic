// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package apikeys

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

var _ datasource.DataSource = &APIKeysDataSource{}

func NewAPIKeysDataSource() datasource.DataSource {
	return &APIKeysDataSource{}
}

type APIKeysDataSource struct {
	adminClient *admin.Client
}

// --- Terraform data models ---

type APIKeysDataSourceModel struct {
	Status          types.String `tfsdk:"status"`
	WorkspaceID     types.String `tfsdk:"workspace_id"`
	CreatedByUserID types.String `tfsdk:"created_by_user_id"`
	APIKeys         types.List   `tfsdk:"api_keys"`
}

// --- API response types ---

type apiKeyListAPIResponse struct {
	Data    []apiKeyAPIResponse `json:"data"`
	HasMore bool                `json:"has_more"`
	FirstID string              `json:"first_id"`
	LastID  string              `json:"last_id"`
}

// --- attr.Type maps ---

// apiKeyListItemAttrTypes mirrors APIKeyResourceModel field for field, so a
// mapped model converts to a list item through types.ObjectValueFrom.
var apiKeyListItemAttrTypes = map[string]attr.Type{
	"id":               types.StringType,
	"name":             types.StringType,
	"status":           types.StringType,
	"workspace_id":     types.StringType,
	"scope":            types.ObjectType{AttrTypes: apiKeyScopeAttrTypes},
	"principal":        types.ObjectType{AttrTypes: apiKeyPrincipalAttrTypes},
	"expires_at":       types.StringType,
	"created_at":       types.StringType,
	"created_by":       types.ObjectType{AttrTypes: apiKeyCreatedByAttrTypes},
	"partial_key_hint": types.StringType,
	"type":             types.StringType,
}

// --- Metadata ---

func (d *APIKeysDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_api_keys"
}

// --- Schema ---

func (d *APIKeysDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists Anthropic API keys via the Admin API. All pages are fetched automatically. " +
			"Results can optionally be filtered by status, workspace and creator. " +
			"Requires `admin_api_key` (or `ANTHROPIC_ADMIN_API_KEY`) to be configured on the provider.",
		Attributes: map[string]schema.Attribute{
			"status": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Filter by key status. Valid values: `active`, `inactive`, `archived`, `expired`.",
				Validators:          []validator.String{stringvalidator.OneOf(apiKeyStatuses...)},
			},
			"workspace_id": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Filter by workspace ID. Returns only keys scoped to the specified workspace.",
			},
			"created_by_user_id": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Filter by the ID of the user who created the key.",
			},
			"api_keys": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "List of API keys matching the specified filters.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Unique API key identifier.",
						},
						"name": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Human-readable name of the API key.",
						},
						"status": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Status of the API key: `active`, `inactive`, `archived` or `expired`.",
						},
						"workspace_id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: apiKeyWorkspaceIDDescription,
							DeprecationMessage:  apiKeyWorkspaceIDDeprecation,
						},
						"scope":     apiKeyScopeDataSourceAttribute(),
						"principal": apiKeyPrincipalDataSourceAttribute(),
						"expires_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: apiKeyExpiresAtDescription,
						},
						"created_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "RFC 3339 timestamp of when the API key was created.",
						},
						"created_by": apiKeyCreatedByDataSourceAttribute(),
						"partial_key_hint": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: apiKeyPartialKeyHintDescription,
						},
						"type": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Object type. Always `api_key`.",
						},
					},
				},
			},
		},
	}
}

// --- Configure ---

func (d *APIKeysDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

	if !providerrors.RequireAdminDataSourceClient(pd.AdminClient, &resp.Diagnostics) {
		return
	}

	d.adminClient = pd.AdminClient
}

// --- Read ---

func (d *APIKeysDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data APIKeysDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	var allKeys []apiKeyAPIResponse
	afterID := ""

	for {
		apiPath := "/v1/organizations/api_keys?" + buildAPIKeysQuery(&data, afterID).Encode()

		respBytes, err := d.adminClient.DoRequest(ctx, "GET", apiPath, nil)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list API keys: %s", err))
			return
		}

		var page apiKeyListAPIResponse
		if err := json.Unmarshal(respBytes, &page); err != nil {
			resp.Diagnostics.AddError("Parse Error", fmt.Sprintf("Unable to parse API keys list response: %s", err))
			return
		}

		allKeys = append(allKeys, page.Data...)

		if !page.HasMore {
			break
		}
		afterID = page.LastID
	}

	keyObjs := make([]attr.Value, 0, len(allKeys))
	for i := range allKeys {
		obj, diags := mapAPIKeyToListObject(ctx, &allKeys[i])
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		keyObjs = append(keyObjs, obj)
	}

	keysList, diags := types.ListValue(types.ObjectType{AttrTypes: apiKeyListItemAttrTypes}, keyObjs)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	data.APIKeys = keysList
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// ============================================================================
// Helper functions
// ============================================================================

// buildAPIKeysQuery builds the query parameters for one page of the list
// request: the fixed page limit, the pagination cursor (omitted when empty)
// and every filter the configuration sets.
func buildAPIKeysQuery(data *APIKeysDataSourceModel, afterID string) url.Values {
	params := url.Values{}
	params.Set("limit", "1000")
	if afterID != "" {
		params.Set("after_id", afterID)
	}
	setIfKnown := func(key string, v types.String) {
		if !v.IsNull() && !v.IsUnknown() {
			params.Set(key, v.ValueString())
		}
	}
	setIfKnown("status", data.Status)
	setIfKnown("workspace_id", data.WorkspaceID)
	setIfKnown("created_by_user_id", data.CreatedByUserID)
	return params
}

// mapAPIKeyToListObject maps one list entry through mapAPIKeyToState, so the
// list items and the single-key resource/data source can never diverge.
func mapAPIKeyToListObject(ctx context.Context, key *apiKeyAPIResponse) (attr.Value, diag.Diagnostics) {
	var model APIKeyResourceModel
	diags := mapAPIKeyToState(ctx, key, &model)
	if diags.HasError() {
		return nil, diags
	}
	obj, d := types.ObjectValueFrom(ctx, apiKeyListItemAttrTypes, model)
	diags.Append(d...)
	return obj, diags
}
