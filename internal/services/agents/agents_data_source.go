// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents

import (
	"context"
	"fmt"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &AgentsDataSource{}

func NewAgentsDataSource() datasource.DataSource {
	return &AgentsDataSource{}
}

// AgentsDataSource defines the data source implementation.
type AgentsDataSource struct {
	client *anthropic.Client
}

// AgentsDataSourceModel describes the data source data model.
type AgentsDataSourceModel struct {
	IncludeArchived types.Bool   `tfsdk:"include_archived"`
	CreatedAtGte    types.String `tfsdk:"created_at_gte"`
	CreatedAtLte    types.String `tfsdk:"created_at_lte"`
	Limit           types.Int64  `tfsdk:"limit"`
	Agents          types.List   `tfsdk:"agents"`
}

// agentListItemAttrTypes describes the attribute types of each element in the
// "agents" list. It mirrors the single-agent data source / resource schema.
var agentListItemAttrTypes = map[string]attr.Type{
	"id":                  types.StringType,
	"name":                types.StringType,
	"model":               types.StringType,
	"model_speed":         types.StringType,
	"model_effort":        types.StringType,
	"model_inference_geo": types.StringType,
	"description":         types.StringType,
	"system":              types.StringType,
	"metadata":            types.MapType{ElemType: types.StringType},
	"mcp_servers":         types.ListType{ElemType: types.ObjectType{AttrTypes: agentMCPServerAttrTypes}},
	"skills":              types.ListType{ElemType: types.ObjectType{AttrTypes: agentSkillAttrTypes}},
	"agent_toolset":       types.ObjectType{AttrTypes: agentToolsetAttrTypes},
	"mcp_toolsets":        types.ListType{ElemType: types.ObjectType{AttrTypes: agentMCPToolsetAttrTypes}},
	"custom_tools":        types.ListType{ElemType: types.ObjectType{AttrTypes: agentCustomToolAttrTypes}},
	"multiagent":          types.ObjectType{AttrTypes: agentMultiagentAttrTypes},
	"execution_identity":  types.ObjectType{AttrTypes: agentExecutionIdentityAttrTypes},
	"version":             types.Int64Type,
	"created_at":          types.StringType,
	"updated_at":          types.StringType,
	"archived_at":         types.StringType,
}

func (d *AgentsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_agents"
}

func (d *AgentsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists Managed Agents on the Anthropic platform (beta). Supports filtering by creation time, archive status, and a per-page limit; all pages are fetched automatically.",
		Attributes: map[string]schema.Attribute{
			// --- Optional inputs ---
			"include_archived": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Whether to include archived agents in the results. Defaults to `false`.",
			},
			"created_at_gte": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Return agents created at or after this RFC 3339 timestamp (inclusive).",
			},
			"created_at_lte": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Return agents created at or before this RFC 3339 timestamp (inclusive).",
			},
			"limit": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "Maximum results per page. Defaults to `20`, maximum `100`. Pagination is handled automatically across all pages.",
			},

			// --- Computed output ---
			"agents": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "List of Managed Agents matching the filters.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Unique agent identifier assigned by the API.",
						},
						"name": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Human-readable name for the agent.",
						},
						"model": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "The model that powers the agent.",
						},
						"model_speed": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Inference speed mode: `standard` or `fast`.",
						},
						"model_effort": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Effort level of the agent's model (`low`, `medium`, `high`, `xhigh` or `max`).",
						},
						"model_inference_geo": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Geographic region pinned for the agent's model requests, or null when unpinned.",
						},
						"description": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Description of what the agent does.",
						},
						"system": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "System prompt for the agent.",
						},
						"metadata": schema.MapAttribute{
							Computed:            true,
							ElementType:         types.StringType,
							MarkdownDescription: "Arbitrary key-value metadata attached to the agent.",
						},
						"mcp_servers": schema.ListNestedAttribute{
							Computed:            true,
							MarkdownDescription: "MCP servers the agent connects to.",
							NestedObject: schema.NestedAttributeObject{
								Attributes: map[string]schema.Attribute{
									"name": schema.StringAttribute{
										Computed:            true,
										MarkdownDescription: "Unique name of the MCP server.",
									},
									"url": schema.StringAttribute{
										Computed:            true,
										MarkdownDescription: "Endpoint URL for the MCP server.",
									},
								},
							},
						},
						"skills": schema.ListNestedAttribute{
							Computed:            true,
							MarkdownDescription: "Skills available to the agent.",
							NestedObject: schema.NestedAttributeObject{
								Attributes: map[string]schema.Attribute{
									"type": schema.StringAttribute{
										Computed:            true,
										MarkdownDescription: "Skill type: `anthropic` or `custom`.",
									},
									"skill_id": schema.StringAttribute{
										Computed:            true,
										MarkdownDescription: "Identifier of the skill.",
									},
									"version": schema.StringAttribute{
										Computed:            true,
										MarkdownDescription: "Pinned version of the skill.",
									},
								},
							},
						},
						"agent_toolset": schema.SingleNestedAttribute{
							Computed:            true,
							MarkdownDescription: "Configuration for built-in agent tools.",
							Attributes: map[string]schema.Attribute{
								"default_enabled": schema.BoolAttribute{
									Computed:            true,
									MarkdownDescription: "Whether tools are enabled by default.",
								},
								"default_permission_policy": schema.StringAttribute{
									Computed:            true,
									MarkdownDescription: "Default permission policy: `always_allow`, `always_ask` or `auto`.",
								},
								"configs": schema.ListNestedAttribute{
									Computed:            true,
									MarkdownDescription: "Per-tool configuration overrides.",
									NestedObject: schema.NestedAttributeObject{
										Attributes: agentToolConfigDataSourceAttributes(),
									},
								},
							},
						},
						"mcp_toolsets": schema.ListNestedAttribute{
							Computed:            true,
							MarkdownDescription: "Tool configurations for MCP servers.",
							NestedObject: schema.NestedAttributeObject{
								Attributes: map[string]schema.Attribute{
									"mcp_server_name": schema.StringAttribute{
										Computed:            true,
										MarkdownDescription: "Name of the MCP server.",
									},
									"default_enabled": schema.BoolAttribute{
										Computed:            true,
										MarkdownDescription: "Whether MCP tools are enabled by default.",
									},
									"default_permission_policy": schema.StringAttribute{
										Computed:            true,
										MarkdownDescription: "Default permission policy: `always_allow`, `always_ask` or `auto`.",
									},
									"configs": schema.ListNestedAttribute{
										Computed:            true,
										MarkdownDescription: "Per-MCP-tool configuration overrides.",
										NestedObject: schema.NestedAttributeObject{
											Attributes: map[string]schema.Attribute{
												"name": schema.StringAttribute{
													Computed:            true,
													MarkdownDescription: "Name of the MCP tool.",
												},
												"enabled": schema.BoolAttribute{
													Computed:            true,
													MarkdownDescription: "Whether this tool is enabled.",
												},
												"permission_policy": schema.StringAttribute{
													Computed:            true,
													MarkdownDescription: "Permission policy: `always_allow`, `always_ask` or `auto`.",
												},
											},
										},
									},
								},
							},
						},
						"custom_tools": schema.ListNestedAttribute{
							Computed:            true,
							MarkdownDescription: "Custom tools executed by the API client.",
							NestedObject: schema.NestedAttributeObject{
								Attributes: map[string]schema.Attribute{
									"name": schema.StringAttribute{
										Computed:            true,
										MarkdownDescription: "Unique tool name.",
									},
									"description": schema.StringAttribute{
										Computed:            true,
										MarkdownDescription: "Description shown to the agent.",
									},
									"input_schema": schema.StringAttribute{
										Computed:            true,
										CustomType:          jsontypes.NormalizedType{},
										MarkdownDescription: "JSON Schema for the tool's input parameters, encoded as a JSON string.",
									},
								},
							},
						},
						"multiagent":         multiagentDataSourceSchema(),
						"execution_identity": executionIdentityDataSourceSchema(),
						"version": schema.Int64Attribute{
							Computed:            true,
							MarkdownDescription: "The agent's current version.",
						},
						"created_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Creation timestamp (RFC 3339).",
						},
						"updated_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Last update timestamp (RFC 3339).",
						},
						"archived_at": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Archive timestamp (RFC 3339). Null if the agent has not been archived.",
						},
					},
				},
			},
		},
	}
}

func (d *AgentsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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

	if !providerrors.RequireDataSourceAPIClient(pd.Client, &resp.Diagnostics) {
		return
	}

	d.client = pd.Client
}

func (d *AgentsDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data AgentsDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	params := anthropic.BetaAgentListParams{}

	if !data.IncludeArchived.IsNull() && !data.IncludeArchived.IsUnknown() {
		params.IncludeArchived = param.NewOpt(data.IncludeArchived.ValueBool())
	}
	if !data.Limit.IsNull() && !data.Limit.IsUnknown() {
		params.Limit = param.NewOpt(data.Limit.ValueInt64())
	}
	if !data.CreatedAtGte.IsNull() && !data.CreatedAtGte.IsUnknown() {
		t, err := time.Parse(time.RFC3339, data.CreatedAtGte.ValueString())
		if err != nil {
			resp.Diagnostics.AddAttributeError(
				path.Root("created_at_gte"),
				"Invalid RFC 3339 timestamp",
				fmt.Sprintf("Failed to parse created_at_gte %q as RFC 3339: %s", data.CreatedAtGte.ValueString(), err),
			)
			return
		}
		params.CreatedAtGte = param.NewOpt(t)
	}
	if !data.CreatedAtLte.IsNull() && !data.CreatedAtLte.IsUnknown() {
		t, err := time.Parse(time.RFC3339, data.CreatedAtLte.ValueString())
		if err != nil {
			resp.Diagnostics.AddAttributeError(
				path.Root("created_at_lte"),
				"Invalid RFC 3339 timestamp",
				fmt.Sprintf("Failed to parse created_at_lte %q as RFC 3339: %s", data.CreatedAtLte.ValueString(), err),
			)
			return
		}
		params.CreatedAtLte = param.NewOpt(t)
	}

	pager := d.client.Beta.Agents.ListAutoPaging(ctx, params)

	agentObjs := make([]attr.Value, 0)
	for pager.Next() {
		a := pager.Current()
		obj, diags := mapAgentToDataSourceObject(&a)
		resp.Diagnostics.Append(diags...)
		if resp.Diagnostics.HasError() {
			return
		}
		agentObjs = append(agentObjs, obj)
	}

	if err := pager.Err(); err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to list agents: %s", err))
		return
	}

	agentsList, diags := types.ListValue(types.ObjectType{AttrTypes: agentListItemAttrTypes}, agentObjs)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	data.Agents = agentsList

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// mapAgentToDataSourceObject converts an API agent response to a Terraform object
// value for inclusion in the agents list: the values of the anthropic_agent
// data source (mapAgentResponseToDataSource), every config the API returns
// included (no state-based filtering).
func mapAgentToDataSourceObject(agent *anthropic.BetaManagedAgentsAgent) (attr.Value, diag.Diagnostics) {
	var data AgentDataSourceModel
	diags := mapAgentResponseToDataSource(agent, &data)
	obj, d := types.ObjectValue(agentListItemAttrTypes, map[string]attr.Value{
		"id":                  data.ID,
		"name":                data.Name,
		"model":               data.Model,
		"model_speed":         data.ModelSpeed,
		"model_effort":        data.ModelEffort,
		"model_inference_geo": data.ModelInferenceGeo,
		"description":         data.Description,
		"system":              data.System,
		"metadata":            data.Metadata,
		"mcp_servers":         data.MCPServers,
		"skills":              data.Skills,
		"agent_toolset":       data.AgentToolset,
		"mcp_toolsets":        data.MCPToolsets,
		"custom_tools":        data.CustomTools,
		"multiagent":          data.Multiagent,
		"execution_identity":  data.ExecutionIdentity,
		"version":             data.Version,
		"created_at":          data.CreatedAt,
		"updated_at":          data.UpdatedAt,
		"archived_at":         data.ArchivedAt,
	})
	diags.Append(d...)
	return obj, diags
}
