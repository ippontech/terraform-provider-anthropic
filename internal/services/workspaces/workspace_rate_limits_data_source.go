// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces

import (
	"context"
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
	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
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
	WorkspaceID      types.String `tfsdk:"workspace_id"`
	GroupType        types.String `tfsdk:"group_type"`
	IncludeInherited types.Bool   `tfsdk:"include_inherited"`
	RateLimits       types.List   `tfsdk:"rate_limits"`
}

// --- API response types ---

// workspaceRateLimitAPIItem is one entry of the list (paginated with
// listRateLimitPages), in the shape the set-overrides endpoint returns too.
type workspaceRateLimitAPIItem struct {
	Type        string                    `json:"type"`
	RateLimitID string                    `json:"rate_limit_id"`
	WorkspaceID string                    `json:"workspace_id"`
	Group       *rateLimitGroupAPI        `json:"group"`
	GroupType   string                    `json:"group_type"`
	Models      []string                  `json:"models"`
	Limits      []workspaceRateLimitEntry `json:"limits"`
}

type workspaceRateLimitEntry struct {
	Type     string                    `json:"type"`
	Value    int64                     `json:"value"`
	OrgLimit *int64                    `json:"org_limit"`
	Source   *workspaceRateLimitSource `json:"source"`
}

type workspaceRateLimitSource struct {
	Type string `json:"type"`
}

// --- attr.Type maps ---

var rateLimitSourceAttrTypes = map[string]attr.Type{
	"type": types.StringType,
}

var rateLimitEntryAttrTypes = map[string]attr.Type{
	"type":      types.StringType,
	"value":     types.Int64Type,
	"org_limit": types.Int64Type,
	"source":    types.ObjectType{AttrTypes: rateLimitSourceAttrTypes},
}

var rateLimitAttrTypes = map[string]attr.Type{
	"type":          types.StringType,
	"rate_limit_id": types.StringType,
	"group":         types.ObjectType{AttrTypes: rateLimitGroupAttrTypes},
	"group_type":    types.StringType,
	"models":        types.ListType{ElemType: types.StringType},
	"limits":        types.ListType{ElemType: types.ObjectType{AttrTypes: rateLimitEntryAttrTypes}},
}

// --- Metadata ---

func (d *WorkspaceRateLimitsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_workspace_rate_limits"
}

// --- Schema ---

func (d *WorkspaceRateLimitsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists a workspace's rate limits. By default only the groups and limiter types with a workspace-level override are returned; " +
			"with `include_inherited = true`, every group with organization-level limits is listed, with the values the workspace inherits as well as its overrides. " +
			"Requires `admin_api_key` (or `ANTHROPIC_ADMIN_API_KEY`) on the provider.",
		Attributes: map[string]schema.Attribute{
			"workspace_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the workspace to list rate limits for.",
			},
			"group_type": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Filter results to a specific group type (" + rateLimitGroupTypes + ").",
			},
			"include_inherited": schema.BoolAttribute{
				Optional: true,
				MarkdownDescription: "Also list the limiter values the workspace inherits from the organization, " +
					"including groups with no workspace-level override; each value's `source.type` tells them apart. Default: `false`.",
			},
			"rate_limits": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Rate-limit entries for this workspace, one per group.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"type": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Object type. Always `workspace_rate_limit`.",
						},
						"rate_limit_id": schema.StringAttribute{
							Computed: true,
							MarkdownDescription: "The `id` of the organization's rate-limit entry this entry applies to: " +
								"the ID that setting a workspace override for this group takes.",
						},
						"group": rateLimitGroupDataSourceAttribute(),
						"group_type": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: rateLimitGroupTypeDescription,
							DeprecationMessage:  rateLimitGroupTypeDeprecation,
						},
						"models": schema.ListAttribute{
							Computed:            true,
							ElementType:         types.StringType,
							MarkdownDescription: "Models included in this limit group (the ones this workspace has access to). Null when `group.type` is not `model_group`.",
						},
						"limits": schema.ListNestedAttribute{
							Computed:            true,
							MarkdownDescription: "The workspace's limiter values for this group.",
							NestedObject: schema.NestedAttributeObject{
								Attributes: map[string]schema.Attribute{
									"type": schema.StringAttribute{
										Computed:            true,
										MarkdownDescription: "Limit type (e.g. `requests_per_minute`).",
									},
									"value": schema.Int64Attribute{
										Computed: true,
										MarkdownDescription: "The workspace's value for this limiter type: its override when `source.type` is `workspace`, " +
											"otherwise the organization's value.",
									},
									"org_limit": schema.Int64Attribute{
										Computed:            true,
										MarkdownDescription: "Organization-level limit, or null if no org limit is configured.",
									},
									"source": schema.SingleNestedAttribute{
										Computed:            true,
										MarkdownDescription: "Where `value` comes from.",
										Attributes: map[string]schema.Attribute{
											"type": schema.StringAttribute{
												Computed: true,
												MarkdownDescription: "`workspace` (a workspace-level override is stored) or `organization` " +
													"(inherited; listed only with `include_inherited = true`).",
											},
										},
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

	groupType := ""
	if !data.GroupType.IsNull() && !data.GroupType.IsUnknown() {
		groupType = data.GroupType.ValueString()
	}
	includeInherited := !data.IncludeInherited.IsNull() && !data.IncludeInherited.IsUnknown() && data.IncludeInherited.ValueBool()

	// Every entry is kept as returned: with include_inherited the API lists
	// groups that have no override at all, and those must not be filtered out.
	allItems, err := listWorkspaceRateLimits(ctx, d.adminClient, data.WorkspaceID.ValueString(), groupType, includeInherited)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list workspace rate limits: %s", err))
		return
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

// workspaceRateLimitsPath is the list endpoint, and the prefix of the
// set-overrides endpoint.
func workspaceRateLimitsPath(workspaceID string) string {
	return "/v1/organizations/workspaces/" + url.PathEscape(workspaceID) + "/rate_limits"
}

// workspaceRateLimitsFilters builds the list filters; include_inherited is
// sent only when true, which is also the API's default when omitted.
func workspaceRateLimitsFilters(groupType string, includeInherited bool) url.Values {
	filters := url.Values{}
	if groupType != "" {
		filters.Set("group_type", groupType)
	}
	if includeInherited {
		filters.Set("include_inherited", "true")
	}
	return filters
}

// listWorkspaceRateLimits fetches every page of
// GET /v1/organizations/workspaces/{workspace_id}/rate_limits.
func listWorkspaceRateLimits(ctx context.Context, client *admin.Client, workspaceID, groupType string, includeInherited bool) ([]workspaceRateLimitAPIItem, error) {
	return listRateLimitPages[workspaceRateLimitAPIItem](ctx, client,
		workspaceRateLimitsPath(workspaceID), workspaceRateLimitsFilters(groupType, includeInherited))
}

// mapRateLimitToObject converts an API rate limit item to a Terraform object value.
func mapRateLimitToObject(ctx context.Context, item workspaceRateLimitAPIItem) (attr.Value, diag.Diagnostics) {
	modelsList, diags := rateLimitModelsValue(ctx, item.Models)

	limitObjs := make([]attr.Value, 0, len(item.Limits))
	for _, entry := range item.Limits {
		orgLimit := types.Int64Null()
		if entry.OrgLimit != nil {
			orgLimit = types.Int64Value(*entry.OrgLimit)
		}
		source := types.ObjectNull(rateLimitSourceAttrTypes)
		if entry.Source != nil {
			var d diag.Diagnostics
			source, d = types.ObjectValue(rateLimitSourceAttrTypes, map[string]attr.Value{
				"type": tfvalue.StringOrNull(entry.Source.Type),
			})
			diags.Append(d...)
		}
		entryObj, d := types.ObjectValue(rateLimitEntryAttrTypes, map[string]attr.Value{
			"type":      types.StringValue(entry.Type),
			"value":     types.Int64Value(entry.Value),
			"org_limit": orgLimit,
			"source":    source,
		})
		diags.Append(d...)
		limitObjs = append(limitObjs, entryObj)
	}

	limitsList, d := types.ListValue(types.ObjectType{AttrTypes: rateLimitEntryAttrTypes}, limitObjs)
	diags.Append(d...)

	group, d := rateLimitGroupValue(item.Group)
	diags.Append(d...)

	obj, d := types.ObjectValue(rateLimitAttrTypes, map[string]attr.Value{
		"type":          types.StringValue(item.Type),
		"rate_limit_id": tfvalue.StringOrNull(item.RateLimitID),
		"group":         group,
		"group_type":    tfvalue.StringOrNull(item.GroupType),
		"models":        modelsList,
		"limits":        limitsList,
	})
	diags.Append(d...)

	return obj, diags
}
