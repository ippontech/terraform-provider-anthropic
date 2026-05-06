// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces

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

var _ datasource.DataSource = &WorkspaceRateLimitsDataSource{}

func NewWorkspaceRateLimitsDataSource() datasource.DataSource {
	return &WorkspaceRateLimitsDataSource{}
}

type WorkspaceRateLimitsDataSource struct {
	adminClient *admin.Client
}

// --- Terraform data models ---

type WorkspaceRateLimitsDataSourceModel struct {
	WorkspaceID types.String `tfsdk:"workspace_id"`
	GroupType   types.String `tfsdk:"group_type"`
	RateLimits  types.List   `tfsdk:"rate_limits"`
}

// --- API response types ---

type workspaceRateLimitsPage struct {
	Data    []workspaceRateLimitAPIItem `json:"data"`
	HasMore bool                        `json:"has_more"`
	FirstID string                      `json:"first_id"`
	LastID  string                      `json:"last_id"`
}

type workspaceRateLimitAPIItem struct {
	Type      string                    `json:"type"`
	GroupType string                    `json:"group_type"`
	Models    []string                  `json:"models"`
	Limits    []workspaceRateLimitEntry `json:"limits"`
}

type workspaceRateLimitEntry struct {
	Type     string `json:"type"`
	Value    int64  `json:"value"`
	OrgLimit *int64 `json:"org_limit"`
}

// --- attr.Type maps ---

var rateLimitEntryAttrTypes = map[string]attr.Type{
	"type":      types.StringType,
	"value":     types.Int64Type,
	"org_limit": types.Int64Type,
}

var rateLimitAttrTypes = map[string]attr.Type{
	"type":       types.StringType,
	"group_type": types.StringType,
	"models":     types.ListType{ElemType: types.StringType},
	"limits":     types.ListType{ElemType: types.ObjectType{AttrTypes: rateLimitEntryAttrTypes}},
}

// --- Metadata ---

func (d *WorkspaceRateLimitsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_workspace_rate_limits"
}

// --- Schema ---

func (d *WorkspaceRateLimitsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists workspace-level rate-limit overrides for a given workspace. " +
			"Only entries that have at least one override are returned; groups inheriting org-level limits are omitted. " +
			"Requires `admin_api_key` (or `ANTHROPIC_ADMIN_API_KEY`) on the provider.",
		Attributes: map[string]schema.Attribute{
			"workspace_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the workspace to list rate-limit overrides for.",
			},
			"group_type": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Filter results to a specific group type (`model_group`, `batch`, `token_count`, `files`, `skills`, `web_search`).",
			},
			"rate_limits": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Rate-limit overrides set for this workspace.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"type": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Object type. Always `workspace_rate_limit`.",
						},
						"group_type": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Rate-limit group type.",
						},
						"models": schema.ListAttribute{
							Computed:            true,
							ElementType:         types.StringType,
							MarkdownDescription: "Models included in this limit group. Null when `group_type` is not `model_group`.",
						},
						"limits": schema.ListNestedAttribute{
							Computed:            true,
							MarkdownDescription: "Individual rate-limit entries.",
							NestedObject: schema.NestedAttributeObject{
								Attributes: map[string]schema.Attribute{
									"type": schema.StringAttribute{
										Computed:            true,
										MarkdownDescription: "Limit type (e.g. `requests_per_minute`).",
									},
									"value": schema.Int64Attribute{
										Computed:            true,
										MarkdownDescription: "Override value applied to this workspace.",
									},
									"org_limit": schema.Int64Attribute{
										Computed:            true,
										MarkdownDescription: "Organisation-level limit, or null if no org limit is configured.",
									},
								},
							},
						},
					},
				},
			},
		},
	}
}

// --- Configure ---

func (d *WorkspaceRateLimitsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *WorkspaceRateLimitsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data WorkspaceRateLimitsDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	workspaceID := data.WorkspaceID.ValueString()
	groupType := ""
	if !data.GroupType.IsNull() && !data.GroupType.IsUnknown() {
		groupType = data.GroupType.ValueString()
	}
	basePath := "/v1/organizations/workspaces/" + workspaceID + "/rate_limits"

	var allItems []workspaceRateLimitAPIItem
	afterID := ""

	for {
		query := url.Values{}
		query.Set("limit", "1000")
		if groupType != "" {
			query.Set("group_type", groupType)
		}
		if afterID != "" {
			query.Set("after_id", afterID)
		}

		apiPath := basePath + "?" + query.Encode()

		respBytes, err := d.adminClient.DoRequest(ctx, "GET", apiPath, nil)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list workspace rate limits: %s", err))
			return
		}

		var page workspaceRateLimitsPage
		if err := json.Unmarshal(respBytes, &page); err != nil {
			resp.Diagnostics.AddError("Parse Error", fmt.Sprintf("Unable to parse workspace rate limits response: %s", err))
			return
		}

		allItems = append(allItems, page.Data...)

		if !page.HasMore {
			break
		}
		afterID = page.LastID
	}

	rateLimitObjs := make([]attr.Value, 0, len(allItems))
	for _, item := range allItems {
		obj, diags := mapRateLimitToObject(ctx, item)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		rateLimitObjs = append(rateLimitObjs, obj)
	}

	rateLimitsList, diags := types.ListValue(types.ObjectType{AttrTypes: rateLimitAttrTypes}, rateLimitObjs)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	data.RateLimits = rateLimitsList
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// mapRateLimitToObject converts an API rate limit item to a Terraform object value.
func mapRateLimitToObject(ctx context.Context, item workspaceRateLimitAPIItem) (attr.Value, diag.Diagnostics) {
	var diags diag.Diagnostics

	var modelsList types.List
	if len(item.Models) == 0 {
		modelsList = types.ListNull(types.StringType)
	} else {
		var d diag.Diagnostics
		modelsList, d = types.ListValueFrom(ctx, types.StringType, item.Models)
		diags.Append(d...)
	}

	limitObjs := make([]attr.Value, 0, len(item.Limits))
	for _, entry := range item.Limits {
		orgLimit := types.Int64Null()
		if entry.OrgLimit != nil {
			orgLimit = types.Int64Value(*entry.OrgLimit)
		}
		entryObj, d := types.ObjectValue(rateLimitEntryAttrTypes, map[string]attr.Value{
			"type":      types.StringValue(entry.Type),
			"value":     types.Int64Value(entry.Value),
			"org_limit": orgLimit,
		})
		diags.Append(d...)
		limitObjs = append(limitObjs, entryObj)
	}

	limitsList, d := types.ListValue(types.ObjectType{AttrTypes: rateLimitEntryAttrTypes}, limitObjs)
	diags.Append(d...)

	obj, d := types.ObjectValue(rateLimitAttrTypes, map[string]attr.Value{
		"type":       types.StringValue(item.Type),
		"group_type": types.StringValue(item.GroupType),
		"models":     modelsList,
		"limits":     limitsList,
	})
	diags.Append(d...)

	return obj, diags
}
