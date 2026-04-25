// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/types"
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
	"id":            types.StringType,
	"name":          types.StringType,
	"model":         types.StringType,
	"model_speed":   types.StringType,
	"description":   types.StringType,
	"system":        types.StringType,
	"metadata":      types.MapType{ElemType: types.StringType},
	"mcp_servers":   types.ListType{ElemType: types.ObjectType{AttrTypes: agentMCPServerAttrTypes}},
	"skills":        types.ListType{ElemType: types.ObjectType{AttrTypes: agentSkillAttrTypes}},
	"agent_toolset": types.ObjectType{AttrTypes: agentToolsetAttrTypes},
	"mcp_toolsets":  types.ListType{ElemType: types.ObjectType{AttrTypes: agentMCPToolsetAttrTypes}},
	"custom_tools":  types.ListType{ElemType: types.ObjectType{AttrTypes: agentCustomToolAttrTypes}},
	"version":       types.Int64Type,
	"created_at":    types.StringType,
	"updated_at":    types.StringType,
	"archived_at":   types.StringType,
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
									MarkdownDescription: "Default permission policy: `always_allow` or `always_ask`.",
								},
								"configs": schema.ListNestedAttribute{
									Computed:            true,
									MarkdownDescription: "Per-tool configuration overrides.",
									NestedObject: schema.NestedAttributeObject{
										Attributes: map[string]schema.Attribute{
											"name": schema.StringAttribute{
												Computed:            true,
												MarkdownDescription: "Built-in tool name.",
											},
											"enabled": schema.BoolAttribute{
												Computed:            true,
												MarkdownDescription: "Whether this tool is enabled.",
											},
											"permission_policy": schema.StringAttribute{
												Computed:            true,
												MarkdownDescription: "Permission policy: `always_allow` or `always_ask`.",
											},
										},
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
										MarkdownDescription: "Default permission policy: `always_allow` or `always_ask`.",
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
													MarkdownDescription: "Permission policy: `always_allow` or `always_ask`.",
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
										MarkdownDescription: "JSON Schema for the tool's input parameters, encoded as a JSON string.",
									},
								},
							},
						},
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

	pd, ok := req.ProviderData.(*ProviderData)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *ProviderData, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
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
// value for inclusion in the agents list. Unlike the resource's Read path, this
// emits ALL configs returned by the API (no state-based filtering).
func mapAgentToDataSourceObject(agent *anthropic.BetaManagedAgentsAgent) (attr.Value, diag.Diagnostics) {
	var diags diag.Diagnostics

	// Model speed: API returns "" when standard; preserve a readable value.
	modelSpeed := types.StringNull()
	if agent.Model.Speed != "" {
		modelSpeed = types.StringValue(string(agent.Model.Speed))
	}

	description := types.StringNull()
	if agent.Description != "" {
		description = types.StringValue(agent.Description)
	}

	system := types.StringNull()
	if agent.System != "" {
		system = types.StringValue(agent.System)
	}

	// Metadata
	metadata := types.MapNull(types.StringType)
	if len(agent.Metadata) > 0 {
		elements := make(map[string]attr.Value, len(agent.Metadata))
		for k, v := range agent.Metadata {
			elements[k] = types.StringValue(v)
		}
		m, d := types.MapValue(types.StringType, elements)
		diags.Append(d...)
		metadata = m
	}

	// MCP servers
	mcpServersList := types.ListNull(types.ObjectType{AttrTypes: agentMCPServerAttrTypes})
	if len(agent.MCPServers) > 0 {
		serverObjs := make([]attr.Value, len(agent.MCPServers))
		for i, s := range agent.MCPServers {
			obj, d := types.ObjectValue(agentMCPServerAttrTypes, map[string]attr.Value{
				"name": types.StringValue(s.Name),
				"url":  types.StringValue(s.URL),
			})
			diags.Append(d...)
			serverObjs[i] = obj
		}
		list, d := types.ListValue(types.ObjectType{AttrTypes: agentMCPServerAttrTypes}, serverObjs)
		diags.Append(d...)
		mcpServersList = list
	}

	// Skills
	skillsList := types.ListNull(types.ObjectType{AttrTypes: agentSkillAttrTypes})
	if len(agent.Skills) > 0 {
		skillObjs := make([]attr.Value, len(agent.Skills))
		for i, s := range agent.Skills {
			obj, d := types.ObjectValue(agentSkillAttrTypes, map[string]attr.Value{
				"type":     types.StringValue(s.Type),
				"skill_id": types.StringValue(s.SkillID),
				"version":  types.StringValue(s.Version),
			})
			diags.Append(d...)
			skillObjs[i] = obj
		}
		list, d := types.ListValue(types.ObjectType{AttrTypes: agentSkillAttrTypes}, skillObjs)
		diags.Append(d...)
		skillsList = list
	}

	// Split tools into the three categories.
	var apiAgentToolset *anthropic.BetaManagedAgentsAgentToolset20260401
	var apiMCPToolsets []anthropic.BetaManagedAgentsMCPToolset
	var apiCustomTools []anthropic.BetaManagedAgentsCustomTool
	for _, tool := range agent.Tools {
		switch tool.Type {
		case "agent_toolset_20260401":
			t := tool.AsAgentToolset20260401()
			apiAgentToolset = &t
		case "mcp_toolset":
			apiMCPToolsets = append(apiMCPToolsets, tool.AsMCPToolset())
		case "custom":
			apiCustomTools = append(apiCustomTools, tool.AsCustom())
		}
	}

	// Agent toolset
	agentToolsetObj := types.ObjectNull(agentToolsetAttrTypes)
	if apiAgentToolset != nil {
		obj, d := mapAgentToolsetToDataSourceObject(apiAgentToolset)
		diags.Append(d...)
		agentToolsetObj = obj
	}

	// MCP toolsets
	mcpToolsetsList := types.ListNull(types.ObjectType{AttrTypes: agentMCPToolsetAttrTypes})
	if len(apiMCPToolsets) > 0 {
		objs := make([]attr.Value, len(apiMCPToolsets))
		for i, t := range apiMCPToolsets {
			obj, d := mapMCPToolsetToDataSourceObject(&t)
			diags.Append(d...)
			objs[i] = obj
		}
		list, d := types.ListValue(types.ObjectType{AttrTypes: agentMCPToolsetAttrTypes}, objs)
		diags.Append(d...)
		mcpToolsetsList = list
	}

	// Custom tools
	customToolsList := types.ListNull(types.ObjectType{AttrTypes: agentCustomToolAttrTypes})
	if len(apiCustomTools) > 0 {
		objs := make([]attr.Value, len(apiCustomTools))
		for i, t := range apiCustomTools {
			inputSchema := types.StringNull()
			if t.InputSchema.Properties != nil || len(t.InputSchema.Required) > 0 || t.InputSchema.Type != "" {
				if schemaJSON, err := json.Marshal(t.InputSchema); err == nil {
					inputSchema = types.StringValue(string(schemaJSON))
				}
			}
			obj, d := types.ObjectValue(agentCustomToolAttrTypes, map[string]attr.Value{
				"name":         types.StringValue(t.Name),
				"description":  types.StringValue(t.Description),
				"input_schema": inputSchema,
			})
			diags.Append(d...)
			objs[i] = obj
		}
		list, d := types.ListValue(types.ObjectType{AttrTypes: agentCustomToolAttrTypes}, objs)
		diags.Append(d...)
		customToolsList = list
	}

	archivedAt := types.StringNull()
	if !agent.ArchivedAt.IsZero() {
		archivedAt = types.StringValue(agent.ArchivedAt.Format(time.RFC3339))
	}

	obj, d := types.ObjectValue(agentListItemAttrTypes, map[string]attr.Value{
		"id":            types.StringValue(agent.ID),
		"name":          types.StringValue(agent.Name),
		"model":         types.StringValue(string(agent.Model.ID)),
		"model_speed":   modelSpeed,
		"description":   description,
		"system":        system,
		"metadata":      metadata,
		"mcp_servers":   mcpServersList,
		"skills":        skillsList,
		"agent_toolset": agentToolsetObj,
		"mcp_toolsets":  mcpToolsetsList,
		"custom_tools":  customToolsList,
		"version":       types.Int64Value(agent.Version),
		"created_at":    types.StringValue(agent.CreatedAt.Format(time.RFC3339)),
		"updated_at":    types.StringValue(agent.UpdatedAt.Format(time.RFC3339)),
		"archived_at":   archivedAt,
	})
	diags.Append(d...)
	return obj, diags
}

// mapAgentToolsetToDataSourceObject converts an API agent toolset to a Terraform
// object. Emits ALL API configs (no state-based filtering).
func mapAgentToolsetToDataSourceObject(apiToolset *anthropic.BetaManagedAgentsAgentToolset20260401) (types.Object, diag.Diagnostics) {
	var diags diag.Diagnostics

	configsList := types.ListNull(types.ObjectType{AttrTypes: agentToolConfigAttrTypes})
	if len(apiToolset.Configs) > 0 {
		configObjs := make([]attr.Value, len(apiToolset.Configs))
		for i, c := range apiToolset.Configs {
			obj, d := types.ObjectValue(agentToolConfigAttrTypes, map[string]attr.Value{
				"name":              types.StringValue(string(c.Name)),
				"enabled":           types.BoolValue(c.Enabled),
				"permission_policy": types.StringValue(c.PermissionPolicy.Type),
			})
			diags.Append(d...)
			configObjs[i] = obj
		}
		list, d := types.ListValue(types.ObjectType{AttrTypes: agentToolConfigAttrTypes}, configObjs)
		diags.Append(d...)
		configsList = list
	}

	obj, d := types.ObjectValue(agentToolsetAttrTypes, map[string]attr.Value{
		"default_enabled":           types.BoolValue(apiToolset.DefaultConfig.Enabled),
		"default_permission_policy": types.StringValue(apiToolset.DefaultConfig.PermissionPolicy.Type),
		"configs":                   configsList,
	})
	diags.Append(d...)
	return obj, diags
}

// mapMCPToolsetToDataSourceObject converts an API MCP toolset to a Terraform
// object. Emits ALL API configs (no state-based filtering).
func mapMCPToolsetToDataSourceObject(apiToolset *anthropic.BetaManagedAgentsMCPToolset) (types.Object, diag.Diagnostics) {
	var diags diag.Diagnostics

	configsList := types.ListNull(types.ObjectType{AttrTypes: agentMCPToolConfigAttrTypes})
	if len(apiToolset.Configs) > 0 {
		configObjs := make([]attr.Value, len(apiToolset.Configs))
		for i, c := range apiToolset.Configs {
			obj, d := types.ObjectValue(agentMCPToolConfigAttrTypes, map[string]attr.Value{
				"name":              types.StringValue(c.Name),
				"enabled":           types.BoolValue(c.Enabled),
				"permission_policy": types.StringValue(c.PermissionPolicy.Type),
			})
			diags.Append(d...)
			configObjs[i] = obj
		}
		list, d := types.ListValue(types.ObjectType{AttrTypes: agentMCPToolConfigAttrTypes}, configObjs)
		diags.Append(d...)
		configsList = list
	}

	obj, d := types.ObjectValue(agentMCPToolsetAttrTypes, map[string]attr.Value{
		"mcp_server_name":           types.StringValue(apiToolset.MCPServerName),
		"default_enabled":           types.BoolValue(apiToolset.DefaultConfig.Enabled),
		"default_permission_policy": types.StringValue(apiToolset.DefaultConfig.PermissionPolicy.Type),
		"configs":                   configsList,
	})
	diags.Append(d...)
	return obj, diags
}
