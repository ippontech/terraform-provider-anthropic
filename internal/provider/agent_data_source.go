// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ datasource.DataSource = &AgentDataSource{}

func NewAgentDataSource() datasource.DataSource {
	return &AgentDataSource{}
}

// AgentDataSource defines the data source implementation.
type AgentDataSource struct {
	client *anthropic.Client
}

// AgentDataSourceModel describes the data source data model.
type AgentDataSourceModel struct {
	AgentID      types.String `tfsdk:"agent_id"`
	ID           types.String `tfsdk:"id"`
	Name         types.String `tfsdk:"name"`
	Model        types.String `tfsdk:"model"`
	ModelSpeed   types.String `tfsdk:"model_speed"`
	Description  types.String `tfsdk:"description"`
	System       types.String `tfsdk:"system"`
	Metadata     types.Map    `tfsdk:"metadata"`
	MCPServers   types.List   `tfsdk:"mcp_servers"`
	Skills       types.List   `tfsdk:"skills"`
	AgentToolset types.Object `tfsdk:"agent_toolset"`
	MCPToolsets  types.List   `tfsdk:"mcp_toolsets"`
	CustomTools  types.List   `tfsdk:"custom_tools"`
	Version      types.Int64  `tfsdk:"version"`
	CreatedAt    types.String `tfsdk:"created_at"`
	UpdatedAt    types.String `tfsdk:"updated_at"`
	ArchivedAt   types.String `tfsdk:"archived_at"`
}

func (d *AgentDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_agent"
}

func (d *AgentDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Retrieves a Managed Agent by ID.",
		Attributes: map[string]schema.Attribute{
			// --- Required ---
			"agent_id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The ID of the agent to retrieve.",
			},

			// --- Computed ---
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
				MarkdownDescription: "Inference speed mode (`standard` or `fast`).",
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
				MarkdownDescription: "MCP servers this agent connects to.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Unique name for this server.",
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
							MarkdownDescription: "Pinned skill version.",
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
						MarkdownDescription: "Default permission policy.",
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
									MarkdownDescription: "Permission policy override.",
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
							MarkdownDescription: "Default permission policy.",
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
										MarkdownDescription: "Permission policy override.",
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
							MarkdownDescription: "Tool name.",
						},
						"description": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Tool description shown to the agent.",
						},
						"input_schema": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "JSON Schema for the tool's input parameters.",
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
	}
}

func (d *AgentDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	client, ok := req.ProviderData.(*anthropic.Client)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Data Source Configure Type",
			fmt.Sprintf("Expected *anthropic.Client, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}

	d.client = client
}

func (d *AgentDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data AgentDataSourceModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	agent, err := d.client.Beta.Agents.Get(ctx, data.AgentID.ValueString(), anthropic.BetaAgentGetParams{})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to retrieve agent: %s", err))
		return
	}

	resp.Diagnostics.Append(mapAgentResponseToDataSource(agent, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// mapAgentResponseToDataSource maps an API agent response to the data source model.
// Unlike the resource mapper, it emits all API-returned configs as-is (no state filtering).
func mapAgentResponseToDataSource(agent *anthropic.BetaManagedAgentsAgent, data *AgentDataSourceModel) diag.Diagnostics {
	var diags diag.Diagnostics

	data.ID = types.StringValue(agent.ID)
	data.Name = types.StringValue(agent.Name)
	data.Model = types.StringValue(string(agent.Model.ID))
	data.Version = types.Int64Value(agent.Version)
	data.CreatedAt = types.StringValue(agent.CreatedAt.Format(time.RFC3339))
	data.UpdatedAt = types.StringValue(agent.UpdatedAt.Format(time.RFC3339))

	if agent.ArchivedAt.IsZero() {
		data.ArchivedAt = types.StringNull()
	} else {
		data.ArchivedAt = types.StringValue(agent.ArchivedAt.Format(time.RFC3339))
	}

	// Model speed: API returns "" for "standard".
	if agent.Model.Speed != "" {
		data.ModelSpeed = types.StringValue(string(agent.Model.Speed))
	} else {
		data.ModelSpeed = types.StringNull()
	}

	// Description / System: empty string maps to null.
	if agent.Description != "" {
		data.Description = types.StringValue(agent.Description)
	} else {
		data.Description = types.StringNull()
	}

	if agent.System != "" {
		data.System = types.StringValue(agent.System)
	} else {
		data.System = types.StringNull()
	}

	// Metadata
	if len(agent.Metadata) > 0 {
		elements := make(map[string]attr.Value, len(agent.Metadata))
		for k, v := range agent.Metadata {
			elements[k] = types.StringValue(v)
		}
		metaMap, d := types.MapValue(types.StringType, elements)
		diags.Append(d...)
		data.Metadata = metaMap
	} else {
		data.Metadata = types.MapNull(types.StringType)
	}

	// MCP servers
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
		serverList, d := types.ListValue(types.ObjectType{AttrTypes: agentMCPServerAttrTypes}, serverObjs)
		diags.Append(d...)
		data.MCPServers = serverList
	} else {
		data.MCPServers = types.ListNull(types.ObjectType{AttrTypes: agentMCPServerAttrTypes})
	}

	// Skills
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
		skillList, d := types.ListValue(types.ObjectType{AttrTypes: agentSkillAttrTypes}, skillObjs)
		diags.Append(d...)
		data.Skills = skillList
	} else {
		data.Skills = types.ListNull(types.ObjectType{AttrTypes: agentSkillAttrTypes})
	}

	// Tools: split API response into agent_toolset, mcp_toolsets, and custom_tools.
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
	if apiAgentToolset != nil {
		obj, d := mapAgentToolsetToDataSource(apiAgentToolset)
		diags.Append(d...)
		data.AgentToolset = obj
	} else {
		data.AgentToolset = types.ObjectNull(agentToolsetAttrTypes)
	}

	// MCP toolsets
	if len(apiMCPToolsets) > 0 {
		list, d := mapMCPToolsetsToDataSource(apiMCPToolsets)
		diags.Append(d...)
		data.MCPToolsets = list
	} else {
		data.MCPToolsets = types.ListNull(types.ObjectType{AttrTypes: agentMCPToolsetAttrTypes})
	}

	// Custom tools
	if len(apiCustomTools) > 0 {
		toolObjs := make([]attr.Value, len(apiCustomTools))
		for i, t := range apiCustomTools {
			inputSchema := types.StringNull()
			if t.InputSchema.Properties != nil || len(t.InputSchema.Required) > 0 || t.InputSchema.Type != "" {
				schemaJSON, err := json.Marshal(t.InputSchema)
				if err == nil {
					inputSchema = types.StringValue(string(schemaJSON))
				}
			}
			obj, d := types.ObjectValue(agentCustomToolAttrTypes, map[string]attr.Value{
				"name":         types.StringValue(t.Name),
				"description":  types.StringValue(t.Description),
				"input_schema": inputSchema,
			})
			diags.Append(d...)
			toolObjs[i] = obj
		}
		toolList, d := types.ListValue(types.ObjectType{AttrTypes: agentCustomToolAttrTypes}, toolObjs)
		diags.Append(d...)
		data.CustomTools = toolList
	} else {
		data.CustomTools = types.ListNull(types.ObjectType{AttrTypes: agentCustomToolAttrTypes})
	}

	return diags
}

// mapAgentToolsetToDataSource maps an API agent toolset to a Terraform object,
// emitting all API-returned configs without state filtering.
func mapAgentToolsetToDataSource(apiToolset *anthropic.BetaManagedAgentsAgentToolset20260401) (types.Object, diag.Diagnostics) {
	var diags diag.Diagnostics

	defaultEnabled := types.BoolValue(apiToolset.DefaultConfig.Enabled)
	defaultPermPolicy := types.StringValue(apiToolset.DefaultConfig.PermissionPolicy.Type)

	configsList := types.ListNull(types.ObjectType{AttrTypes: agentToolConfigAttrTypes})
	if len(apiToolset.Configs) > 0 {
		configObjs := make([]attr.Value, 0, len(apiToolset.Configs))
		for _, c := range apiToolset.Configs {
			obj, d := types.ObjectValue(agentToolConfigAttrTypes, map[string]attr.Value{
				"name":              types.StringValue(string(c.Name)),
				"enabled":           types.BoolValue(c.Enabled),
				"permission_policy": types.StringValue(c.PermissionPolicy.Type),
			})
			diags.Append(d...)
			configObjs = append(configObjs, obj)
		}
		list, d := types.ListValue(types.ObjectType{AttrTypes: agentToolConfigAttrTypes}, configObjs)
		diags.Append(d...)
		configsList = list
	}

	obj, d := types.ObjectValue(agentToolsetAttrTypes, map[string]attr.Value{
		"default_enabled":           defaultEnabled,
		"default_permission_policy": defaultPermPolicy,
		"configs":                   configsList,
	})
	diags.Append(d...)
	return obj, diags
}

// mapMCPToolsetsToDataSource maps API MCP toolsets to a Terraform list,
// emitting all API-returned toolsets and configs without state filtering.
func mapMCPToolsetsToDataSource(apiToolsets []anthropic.BetaManagedAgentsMCPToolset) (types.List, diag.Diagnostics) {
	var diags diag.Diagnostics

	toolsetObjs := make([]attr.Value, 0, len(apiToolsets))
	for _, apiToolset := range apiToolsets {
		defaultEnabled := types.BoolValue(apiToolset.DefaultConfig.Enabled)
		defaultPermPolicy := types.StringValue(apiToolset.DefaultConfig.PermissionPolicy.Type)

		configsList := types.ListNull(types.ObjectType{AttrTypes: agentMCPToolConfigAttrTypes})
		if len(apiToolset.Configs) > 0 {
			configObjs := make([]attr.Value, 0, len(apiToolset.Configs))
			for _, c := range apiToolset.Configs {
				obj, d := types.ObjectValue(agentMCPToolConfigAttrTypes, map[string]attr.Value{
					"name":              types.StringValue(c.Name),
					"enabled":           types.BoolValue(c.Enabled),
					"permission_policy": types.StringValue(c.PermissionPolicy.Type),
				})
				diags.Append(d...)
				configObjs = append(configObjs, obj)
			}
			list, d := types.ListValue(types.ObjectType{AttrTypes: agentMCPToolConfigAttrTypes}, configObjs)
			diags.Append(d...)
			configsList = list
		}

		obj, d := types.ObjectValue(agentMCPToolsetAttrTypes, map[string]attr.Value{
			"mcp_server_name":           types.StringValue(apiToolset.MCPServerName),
			"default_enabled":           defaultEnabled,
			"default_permission_policy": defaultPermPolicy,
			"configs":                   configsList,
		})
		diags.Append(d...)
		toolsetObjs = append(toolsetObjs, obj)
	}

	list, d := types.ListValue(types.ObjectType{AttrTypes: agentMCPToolsetAttrTypes}, toolsetObjs)
	diags.Append(d...)
	return list, diags
}
