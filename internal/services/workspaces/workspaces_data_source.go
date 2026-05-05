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

var _ datasource.DataSource = &WorkspacesDataSource{}

func NewWorkspacesDataSource() datasource.DataSource {
	return &WorkspacesDataSource{}
}

type WorkspacesDataSource struct {
	adminClient *admin.Client
}

type WorkspacesDataSourceModel struct {
	IncludeArchived types.Bool `tfsdk:"include_archived"`
	Workspaces      types.List `tfsdk:"workspaces"`
}

var workspaceListDataResidencyAttrTypes = map[string]attr.Type{
	"allowed_inference_geos": types.ListType{ElemType: types.StringType},
	"default_inference_geo":  types.StringType,
	"workspace_geo":          types.StringType,
}

var workspaceListItemAttrTypes = map[string]attr.Type{
	"id":             types.StringType,
	"name":           types.StringType,
	"data_residency": types.ObjectType{AttrTypes: workspaceListDataResidencyAttrTypes},
	"archived_at":    types.StringType,
	"created_at":     types.StringType,
	"display_color":  types.StringType,
	"type":           types.StringType,
}

// workspaceListAPIResponse is the paginated list response from GET /v1/organizations/workspaces.
type workspaceListAPIResponse struct {
	Data    []workspaceAPIResponse `json:"data"`
	HasMore bool                   `json:"has_more"`
	LastID  *string                `json:"last_id"`
}

func (d *WorkspacesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_workspaces"
}

func (d *WorkspacesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists all Anthropic Workspaces via the Admin API. All pages are fetched automatically. " +
			"Requires `admin_api_key` (or `ANTHROPIC_ADMIN_API_KEY`) to be configured on the provider.",
		Attributes: map[string]schema.Attribute{
			"include_archived": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "When true, archived workspaces are included in the results.",
			},
			"workspaces": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "List of workspaces.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Unique workspace identifier.",
						},
						"name": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Human-readable name for the workspace.",
						},
						"data_residency": schema.SingleNestedAttribute{
							Computed:            true,
							MarkdownDescription: "Data-residency configuration for the workspace.",
							Attributes: map[string]schema.Attribute{
								"allowed_inference_geos": schema.ListAttribute{
									Computed:            true,
									ElementType:         types.StringType,
									MarkdownDescription: "Permitted inference geo values.",
								},
								"default_inference_geo": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "Default inference geo applied when requests omit the parameter.",
								},
								"workspace_geo": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "Geographic region for workspace data storage.",
								},
							},
						},
						"archived_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "RFC 3339 timestamp of when the workspace was archived, or null if active.",
						},
						"created_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "RFC 3339 timestamp of when the workspace was created.",
						},
						"display_color": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Hex color code representing the workspace in the Anthropic Console.",
						},
						"type": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Object type. Always `workspace`.",
						},
					},
				},
			},
		},
	}
}

func (d *WorkspacesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

func (d *WorkspacesDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data WorkspacesDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	wsObjs := make([]attr.Value, 0)
	var afterID string

	for {
		params := url.Values{}
		params.Set("limit", "1000")
		if afterID != "" {
			params.Set("after_id", afterID)
		}
		if !data.IncludeArchived.IsNull() && !data.IncludeArchived.IsUnknown() && data.IncludeArchived.ValueBool() {
			params.Set("include_archived", "true")
		}

		path := "/v1/organizations/workspaces?" + params.Encode()
		respBytes, err := d.adminClient.DoRequest(ctx, "GET", path, nil)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list workspaces: %s", err))
			return
		}

		var page workspaceListAPIResponse
		if err := json.Unmarshal(respBytes, &page); err != nil {
			resp.Diagnostics.AddError("Parse Error", fmt.Sprintf("Unable to parse workspaces list response: %s", err))
			return
		}

		for i := range page.Data {
			ws := &page.Data[i]
			obj, diags := mapWorkspaceToListObject(ws)
			resp.Diagnostics.Append(diags...)
			if resp.Diagnostics.HasError() {
				return
			}
			wsObjs = append(wsObjs, obj)
		}

		if !page.HasMore || page.LastID == nil {
			break
		}
		afterID = *page.LastID
	}

	wsList, diags := types.ListValue(types.ObjectType{AttrTypes: workspaceListItemAttrTypes}, wsObjs)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.Workspaces = wsList

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func mapWorkspaceToListObject(ws *workspaceAPIResponse) (attr.Value, diag.Diagnostics) {
	var diags diag.Diagnostics

	geos, err := parseAllowedInferenceGeos(ws.DataResidency.AllowedInferenceGeos)
	if err != nil {
		diags.AddError("Parse Error", fmt.Sprintf("Unable to parse allowed_inference_geos: %s", err))
		return nil, diags
	}

	var allowedGeosList types.List
	if len(geos) == 0 {
		allowedGeosList = types.ListNull(types.StringType)
	} else {
		geoElems := make([]attr.Value, len(geos))
		for i, g := range geos {
			geoElems[i] = types.StringValue(g)
		}
		var d diag.Diagnostics
		allowedGeosList, d = types.ListValue(types.StringType, geoElems)
		diags.Append(d...)
		if diags.HasError() {
			return nil, diags
		}
	}

	drObj, d := types.ObjectValue(workspaceListDataResidencyAttrTypes, map[string]attr.Value{
		"allowed_inference_geos": allowedGeosList,
		"default_inference_geo":  types.StringValue(ws.DataResidency.DefaultInferenceGeo),
		"workspace_geo":          types.StringValue(ws.DataResidency.WorkspaceGeo),
	})
	diags.Append(d...)
	if diags.HasError() {
		return nil, diags
	}

	archivedAt := types.StringNull()
	if ws.ArchivedAt != nil && *ws.ArchivedAt != "" {
		archivedAt = types.StringValue(*ws.ArchivedAt)
	}

	obj, d := types.ObjectValue(workspaceListItemAttrTypes, map[string]attr.Value{
		"id":             types.StringValue(ws.ID),
		"name":           types.StringValue(ws.Name),
		"data_residency": drObj,
		"archived_at":    archivedAt,
		"created_at":     types.StringValue(ws.CreatedAt),
		"display_color":  types.StringValue(ws.DisplayColor),
		"type":           types.StringValue(ws.Type),
	})
	diags.Append(d...)
	return obj, diags
}
