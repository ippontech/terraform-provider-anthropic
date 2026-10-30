// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/types"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
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
	AgentID           types.String `tfsdk:"agent_id"`
	ID                types.String `tfsdk:"id"`
	Name              types.String `tfsdk:"name"`
	Model             types.String `tfsdk:"model"`
	ModelSpeed        types.String `tfsdk:"model_speed"`
	ModelEffort       types.String `tfsdk:"model_effort"`
	ModelInferenceGeo types.String `tfsdk:"model_inference_geo"`
	Description       types.String `tfsdk:"description"`
	System            types.String `tfsdk:"system"`
	Metadata          types.Map    `tfsdk:"metadata"`
	MCPServers        types.List   `tfsdk:"mcp_servers"`
	Skills            types.List   `tfsdk:"skills"`
	AgentToolset      types.Object `tfsdk:"agent_toolset"`
	MCPToolsets       types.List   `tfsdk:"mcp_toolsets"`
	CustomTools       types.List   `tfsdk:"custom_tools"`
	Multiagent        types.Object `tfsdk:"multiagent"`
	ExecutionIdentity types.Object `tfsdk:"execution_identity"`
	Version           types.Int64  `tfsdk:"version"`
	CreatedAt         types.String `tfsdk:"created_at"`
	UpdatedAt         types.String `tfsdk:"updated_at"`
	ArchivedAt        types.String `tfsdk:"archived_at"`
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
							MarkdownDescription: "Tool name.",
						},
						"description": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "Tool description shown to the agent.",
						},
						"input_schema": schema.StringAttribute{
							Computed:            true,
							CustomType:          jsontypes.NormalizedType{},
							MarkdownDescription: "JSON Schema for the tool's input parameters.",
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
	}
}

func (d *AgentDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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
	data.ID = types.StringValue(agent.ID)
	data.Name = types.StringValue(agent.Name)
	data.Model = types.StringValue(string(agent.Model.ID))
	data.Version = types.Int64Value(agent.Version)
	data.CreatedAt = formatTime(agent.CreatedAt)
	data.UpdatedAt = formatTime(agent.UpdatedAt)
	data.ArchivedAt = tfvalue.TimeOrNull(agent.ArchivedAt)
	// Model speed: API returns "" for "standard".
	data.ModelSpeed = tfvalue.StringOrNull(string(agent.Model.Speed))
	data.ModelEffort = modelEffortFromResponse(agent.Model)
	data.ModelInferenceGeo = modelInferenceGeoFromResponse(agent.Model)
	data.Description = tfvalue.StringOrNull(agent.Description)
	data.System = tfvalue.StringOrNull(agent.System)

	var diags, d diag.Diagnostics
	data.Metadata, d = metadataValue(agent.Metadata)
	diags.Append(d...)
	data.MCPServers, d = mcpServersValue(agent.MCPServers)
	diags.Append(d...)
	data.Skills, d = skillsValue(agent.Skills)
	diags.Append(d...)

	tools := splitAgentTools(agent.Tools)
	data.AgentToolset, d = mapAgentToolsetToDataSource(tools.toolset)
	diags.Append(d...)
	data.MCPToolsets, d = mapMCPToolsetsToDataSource(tools.mcp)
	diags.Append(d...)
	data.CustomTools, d = customToolsValue(tools.custom, rawInputSchema)
	diags.Append(d...)

	// Multiagent (verbatim from the API)
	data.Multiagent, d = mapMultiagentToObject(agent.Multiagent, agent.ID)
	diags.Append(d...)

	// Execution identity (verbatim from the API, `service_account` included)
	data.ExecutionIdentity, d = executionIdentityObject(agent.ExecutionIdentity)
	diags.Append(d...)
	return diags
}

// agentToolConfigDataSourceAttributes describes an `agent_toolset.configs`
// entry, shared by the agent and agents data sources.
func agentToolConfigDataSourceAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
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
			MarkdownDescription: "Permission policy: `always_allow`, `always_ask` or `auto`.",
		},
		"allowed_domains": schema.SetAttribute{
			Computed:            true,
			ElementType:         types.StringType,
			MarkdownDescription: "The only hosts the tool can reach (`web_fetch` and `web_search`), null when unrestricted.",
		},
		"blocked_domains": schema.SetAttribute{
			Computed:            true,
			ElementType:         types.StringType,
			MarkdownDescription: "Hosts the tool can never reach (`web_fetch` and `web_search`), null when none.",
		},
		"max_content_tokens": schema.Int64Attribute{
			Computed:            true,
			MarkdownDescription: "Maximum number of tokens of fetched text content included in the context per call (`web_fetch`), null when unset.",
		},
		"user_location": schema.SingleNestedAttribute{
			Computed:            true,
			MarkdownDescription: "Approximate user location used to localize search results (`web_search`), null when unset.",
			Attributes: map[string]schema.Attribute{
				"country":  schema.StringAttribute{Computed: true, MarkdownDescription: "Two-letter ISO 3166-1 country code."},
				"city":     schema.StringAttribute{Computed: true, MarkdownDescription: "City name."},
				"region":   schema.StringAttribute{Computed: true, MarkdownDescription: "Region or state name."},
				"timezone": schema.StringAttribute{Computed: true, MarkdownDescription: "IANA timezone identifier."},
			},
		},
		"url_sources": urlSourcesDataSourceSchema(),
	}
}

// mapAgentToolsetToDataSource maps an API agent toolset to a Terraform object,
// emitting all API-returned configs without state filtering; null when the
// agent has none.
func mapAgentToolsetToDataSource(apiToolset *anthropic.BetaManagedAgentsAgentToolset20260401) (types.Object, diag.Diagnostics) {
	var diags diag.Diagnostics
	if apiToolset == nil {
		return types.ObjectNull(agentToolsetAttrTypes), diags
	}

	defaultEnabled := types.BoolValue(apiToolset.DefaultConfig.Enabled)
	defaultPermPolicy := types.StringValue(apiToolset.DefaultConfig.PermissionPolicy.Type)

	configsList := types.ListNull(types.ObjectType{AttrTypes: agentToolConfigAttrTypes})
	if len(apiToolset.Configs) > 0 {
		configObjs := make([]attr.Value, 0, len(apiToolset.Configs))
		for _, c := range apiToolset.Configs {
			obj, d := agentToolConfigObject(c)
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
// emitting all API-returned toolsets and configs without state filtering;
// null when there are none.
func mapMCPToolsetsToDataSource(apiToolsets []anthropic.BetaManagedAgentsMCPToolset) (types.List, diag.Diagnostics) {
	var diags diag.Diagnostics
	if len(apiToolsets) == 0 {
		return types.ListNull(types.ObjectType{AttrTypes: agentMCPToolsetAttrTypes}), diags
	}

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
