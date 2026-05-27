// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package apikeys

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
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
	Status      types.String `tfsdk:"status"`
	WorkspaceID types.String `tfsdk:"workspace_id"`
	APIKeys     types.List   `tfsdk:"api_keys"`
}

// --- API response types ---

type apiKeyListAPIResponse struct {
	Data    []apiKeyAPIResponse `json:"data"`
	HasMore bool                `json:"has_more"`
	FirstID string              `json:"first_id"`
	LastID  string              `json:"last_id"`
}

// --- attr.Type maps ---

var apiKeyListItemAttrTypes = map[string]attr.Type{
	"id":               types.StringType,
	"name":             types.StringType,
	"status":           types.StringType,
	"workspace_id":     types.StringType,
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
			"Results can optionally be filtered by status and workspace. " +
			"Requires `admin_api_key` (or `ANTHROPIC_ADMIN_API_KEY`) to be configured on the provider.",
		Attributes: map[string]schema.Attribute{
			"status": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Filter by key status. Valid values: `active`, `inactive`.",
			},
			"workspace_id": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Filter by workspace ID. Returns only keys scoped to the specified workspace.",
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
							MarkdownDescription: "Status of the API key (`active` or `inactive`).",
						},
						"workspace_id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "The ID of the workspace this key is scoped to, or null for organization-level keys.",
						},
						"created_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "RFC 3339 timestamp of when the API key was created.",
						},
						"created_by": schema.SingleNestedAttribute{
							Computed:            true,
							MarkdownDescription: "Details about the actor who created this API key.",
							Attributes: map[string]schema.Attribute{
								"id": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "Identifier of the creator.",
								},
								"type": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "Type of the creator (e.g. `user`).",
								},
							},
						},
						"partial_key_hint": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "The last four visible characters of the API key, for identification.",
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
		params := url.Values{}
		params.Set("limit", "1000")
		if afterID != "" {
			params.Set("after_id", afterID)
		}
		if !data.Status.IsNull() && !data.Status.IsUnknown() {
			params.Set("status", data.Status.ValueString())
		}
		if !data.WorkspaceID.IsNull() && !data.WorkspaceID.IsUnknown() {
			params.Set("workspace_id", data.WorkspaceID.ValueString())
		}

		apiPath := "/v1/organizations/api_keys?" + params.Encode()

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
		obj, diags := mapAPIKeyToListObject(&allKeys[i])
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

func mapAPIKeyToListObject(key *apiKeyAPIResponse) (attr.Value, diag.Diagnostics) {
	var diags diag.Diagnostics

	wsID := types.StringNull()
	if key.WorkspaceID != nil {
		wsID = types.StringValue(*key.WorkspaceID)
	}

	createdByObj, d := types.ObjectValue(apiKeyCreatedByAttrTypes, map[string]attr.Value{
		"id":   types.StringValue(key.CreatedBy.ID),
		"type": types.StringValue(key.CreatedBy.Type),
	})
	diags.Append(d...)
	if diags.HasError() {
		return nil, diags
	}

	obj, d := types.ObjectValue(apiKeyListItemAttrTypes, map[string]attr.Value{
		"id":               types.StringValue(key.ID),
		"name":             types.StringValue(key.Name),
		"status":           types.StringValue(key.Status),
		"workspace_id":     wsID,
		"created_at":       types.StringValue(key.CreatedAt),
		"created_by":       createdByObj,
		"partial_key_hint": types.StringValue(key.PartialKeyHint),
		"type":             types.StringValue(key.Type),
	})
	diags.Append(d...)
	return obj, diags
}
