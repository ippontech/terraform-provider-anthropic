// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	providerrors "github.com/ippontech/terraform-provider-anthropic/internal/errors"
	providerdata "github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &AgentResource{}
var _ resource.ResourceWithImportState = &AgentResource{}

func NewAgentResource() resource.Resource {
	return &AgentResource{}
}

// AgentResource defines the resource implementation.
type AgentResource struct {
	client *anthropic.Client
}

// --- Terraform data models ---

type AgentResourceModel struct {
	Model        types.String `tfsdk:"model"`
	Name         types.String `tfsdk:"name"`
	ModelSpeed   types.String `tfsdk:"model_speed"`
	Description  types.String `tfsdk:"description"`
	System       types.String `tfsdk:"system"`
	Metadata     types.Map    `tfsdk:"metadata"`
	MCPServers   types.List   `tfsdk:"mcp_servers"`
	Skills       types.List   `tfsdk:"skills"`
	AgentToolset types.Object `tfsdk:"agent_toolset"`
	MCPToolsets  types.List   `tfsdk:"mcp_toolsets"`
	CustomTools  types.List   `tfsdk:"custom_tools"`
	ID           types.String `tfsdk:"id"`
	Version      types.Int64  `tfsdk:"version"`
	CreatedAt    types.String `tfsdk:"created_at"`
	UpdatedAt    types.String `tfsdk:"updated_at"`
	ArchivedAt   types.String `tfsdk:"archived_at"`
}

type agentMCPServerModel struct {
	Name types.String `tfsdk:"name"`
	URL  types.String `tfsdk:"url"`
}

type agentSkillModel struct {
	Type    types.String `tfsdk:"type"`
	SkillID types.String `tfsdk:"skill_id"`
	Version types.String `tfsdk:"version"`
}

type agentToolConfigModel struct {
	Name             types.String `tfsdk:"name"`
	Enabled          types.Bool   `tfsdk:"enabled"`
	PermissionPolicy types.String `tfsdk:"permission_policy"`
}

type agentToolsetModel struct {
	DefaultEnabled          types.Bool   `tfsdk:"default_enabled"`
	DefaultPermissionPolicy types.String `tfsdk:"default_permission_policy"`
	Configs                 types.List   `tfsdk:"configs"`
}

type agentMCPToolsetModel struct {
	MCPServerName           types.String `tfsdk:"mcp_server_name"`
	DefaultEnabled          types.Bool   `tfsdk:"default_enabled"`
	DefaultPermissionPolicy types.String `tfsdk:"default_permission_policy"`
	Configs                 types.List   `tfsdk:"configs"`
}

type agentCustomToolModel struct {
	Name        types.String         `tfsdk:"name"`
	Description types.String         `tfsdk:"description"`
	InputSchema jsontypes.Normalized `tfsdk:"input_schema"`
}

// --- Attribute type maps for nested objects ---

var agentToolConfigAttrTypes = map[string]attr.Type{
	"name":              types.StringType,
	"enabled":           types.BoolType,
	"permission_policy": types.StringType,
}

var agentToolsetAttrTypes = map[string]attr.Type{
	"default_enabled":           types.BoolType,
	"default_permission_policy": types.StringType,
	"configs":                   types.ListType{ElemType: types.ObjectType{AttrTypes: agentToolConfigAttrTypes}},
}

var agentMCPServerAttrTypes = map[string]attr.Type{
	"name": types.StringType,
	"url":  types.StringType,
}

var agentSkillAttrTypes = map[string]attr.Type{
	"type":     types.StringType,
	"skill_id": types.StringType,
	"version":  types.StringType,
}

var agentMCPToolConfigAttrTypes = map[string]attr.Type{
	"name":              types.StringType,
	"enabled":           types.BoolType,
	"permission_policy": types.StringType,
}

var agentMCPToolsetAttrTypes = map[string]attr.Type{
	"mcp_server_name":           types.StringType,
	"default_enabled":           types.BoolType,
	"default_permission_policy": types.StringType,
	"configs":                   types.ListType{ElemType: types.ObjectType{AttrTypes: agentMCPToolConfigAttrTypes}},
}

var agentCustomToolAttrTypes = map[string]attr.Type{
	"name":         types.StringType,
	"description":  types.StringType,
	"input_schema": jsontypes.NormalizedType{},
}

// --- Permission policy validators (reused across tool schemas) ---

var permissionPolicyValidator = stringvalidator.OneOf("always_allow", "always_ask")

// --- Schema ---

func (r *AgentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_agent"
}

func (r *AgentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Creates and manages a Managed Agent on the Anthropic platform (beta). " +
			"Agents are persistent configurations that can be used to run sessions with tools, MCP servers, and skills.",
		Attributes: map[string]schema.Attribute{
			// --- Required ---
			"model": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The model that will power the agent (e.g. `claude-sonnet-4-6`).",
			},
			"name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Human-readable name for the agent. 1-256 characters.",
			},

			// --- Optional ---
			"model_speed": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Inference speed mode. `fast` provides faster output at premium pricing. Not all models support `fast`.",
				Validators:          []validator.String{stringvalidator.OneOf("standard", "fast")},
			},
			"description": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Description of what the agent does. Up to 2048 characters.",
			},
			"system": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "System prompt for the agent. Up to 100,000 characters.",
			},
			"metadata": schema.MapAttribute{
				Optional:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Arbitrary key-value metadata. Maximum 16 pairs, keys up to 64 chars, values up to 512 chars.",
			},
			"mcp_servers": schema.ListNestedAttribute{
				Optional:            true,
				MarkdownDescription: "MCP servers this agent connects to. Maximum 20. Names must be unique.",
				Validators:          []validator.List{listvalidator.SizeAtMost(20)},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Required:            true,
							MarkdownDescription: "Unique name for this server, referenced by `mcp_toolsets`. 1-255 characters.",
						},
						"url": schema.StringAttribute{
							Required:            true,
							MarkdownDescription: "Endpoint URL for the MCP server.",
						},
					},
				},
			},
			"skills": schema.ListNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Skills available to the agent. Maximum 20.",
				Validators:          []validator.List{listvalidator.SizeAtMost(20)},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"type": schema.StringAttribute{
							Required:            true,
							MarkdownDescription: "Skill type: `anthropic` (managed by Anthropic) or `custom` (user-created).",
							Validators:          []validator.String{stringvalidator.OneOf("anthropic", "custom")},
						},
						"skill_id": schema.StringAttribute{
							Required:            true,
							MarkdownDescription: "Identifier of the skill (e.g. `xlsx` for Anthropic skills, or a tagged ID for custom skills).",
						},
						"version": schema.StringAttribute{
							Optional:            true,
							Computed:            true,
							MarkdownDescription: "Version to pin. Defaults to latest if omitted.",
						},
					},
				},
			},
			"agent_toolset": schema.SingleNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Configuration for built-in agent tools (bash, edit, read, write, glob, grep, web_fetch, web_search).",
				Attributes: map[string]schema.Attribute{
					"default_enabled": schema.BoolAttribute{
						Optional:            true,
						Computed:            true,
						MarkdownDescription: "Whether tools are enabled by default. Defaults to `true`.",
					},
					"default_permission_policy": schema.StringAttribute{
						Optional:            true,
						Computed:            true,
						MarkdownDescription: "Default permission policy: `always_allow` or `always_ask`.",
						Validators:          []validator.String{permissionPolicyValidator},
					},
					"configs": schema.ListNestedAttribute{
						Optional:            true,
						MarkdownDescription: "Per-tool configuration overrides.",
						NestedObject: schema.NestedAttributeObject{
							Attributes: map[string]schema.Attribute{
								"name": schema.StringAttribute{
									Required:            true,
									MarkdownDescription: "Built-in tool name.",
									Validators: []validator.String{stringvalidator.OneOf(
										"bash", "edit", "read", "write", "glob", "grep", "web_fetch", "web_search",
									)},
								},
								"enabled": schema.BoolAttribute{
									Optional:            true,
									MarkdownDescription: "Whether this tool is enabled. Overrides `default_enabled`.",
								},
								"permission_policy": schema.StringAttribute{
									Optional:            true,
									MarkdownDescription: "Permission policy override: `always_allow` or `always_ask`.",
									Validators:          []validator.String{permissionPolicyValidator},
								},
							},
						},
					},
				},
			},
			"mcp_toolsets": schema.ListNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Tool configurations for MCP servers. Each entry corresponds to a server defined in `mcp_servers`.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"mcp_server_name": schema.StringAttribute{
							Required:            true,
							MarkdownDescription: "Name of the MCP server (must match a name in `mcp_servers`).",
						},
						"default_enabled": schema.BoolAttribute{
							Optional:            true,
							Computed:            true,
							MarkdownDescription: "Whether MCP tools are enabled by default.",
						},
						"default_permission_policy": schema.StringAttribute{
							Optional:            true,
							Computed:            true,
							MarkdownDescription: "Default permission policy: `always_allow` or `always_ask`.",
							Validators:          []validator.String{permissionPolicyValidator},
						},
						"configs": schema.ListNestedAttribute{
							Optional:            true,
							MarkdownDescription: "Per-MCP-tool configuration overrides.",
							NestedObject: schema.NestedAttributeObject{
								Attributes: map[string]schema.Attribute{
									"name": schema.StringAttribute{
										Required:            true,
										MarkdownDescription: "Name of the MCP tool to configure.",
									},
									"enabled": schema.BoolAttribute{
										Optional:            true,
										MarkdownDescription: "Whether this tool is enabled.",
									},
									"permission_policy": schema.StringAttribute{
										Optional:            true,
										MarkdownDescription: "Permission policy override: `always_allow` or `always_ask`.",
										Validators:          []validator.String{permissionPolicyValidator},
									},
								},
							},
						},
					},
				},
			},
			"custom_tools": schema.ListNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Custom tools executed by the API client. When the agent calls a custom tool, the session goes idle until the client provides the result.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"name": schema.StringAttribute{
							Required:            true,
							MarkdownDescription: "Unique tool name. 1-128 characters; letters, digits, underscores, and hyphens.",
						},
						"description": schema.StringAttribute{
							Required:            true,
							MarkdownDescription: "Description shown to the agent. 1-1024 characters.",
						},
						"input_schema": schema.StringAttribute{
							Optional:            true,
							CustomType:          jsontypes.NormalizedType{},
							MarkdownDescription: "JSON Schema for the tool's input parameters. Use `jsonencode()` to build the value.",
						},
					},
				},
			},

			// --- Computed ---
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Unique agent identifier assigned by the API.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"version": schema.Int64Attribute{
				Computed:            true,
				MarkdownDescription: "The agent's current version. Starts at 1 and increments on each modification.",
			},
			"created_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Creation timestamp (RFC 3339).",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"updated_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Last update timestamp (RFC 3339).",
			},
			"archived_at": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Archive timestamp (RFC 3339). Null if the agent has not been archived.",
				PlanModifiers:       []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
		},
	}
}

// --- Configure ---

func (r *AgentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	pd, ok := req.ProviderData.(*providerdata.ProviderData)
	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *providerdata.ProviderData, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)
		return
	}

	if !providerrors.RequireResourceAPIClient(pd.Client, &resp.Diagnostics) {
		return
	}

	r.client = pd.Client
}

// --- Create ---

func (r *AgentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data AgentResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	params := anthropic.BetaAgentNewParams{
		Model: anthropic.BetaManagedAgentsModelConfigParams{
			ID: anthropic.BetaManagedAgentsModel(data.Model.ValueString()),
		},
		Name: data.Name.ValueString(),
	}

	if !data.ModelSpeed.IsNull() && !data.ModelSpeed.IsUnknown() {
		params.Model.Speed = anthropic.BetaManagedAgentsModelConfigParamsSpeed(data.ModelSpeed.ValueString())
	}
	if !data.Description.IsNull() {
		params.Description = param.NewOpt(data.Description.ValueString())
	}
	if !data.System.IsNull() {
		params.System = param.NewOpt(data.System.ValueString())
	}

	// Metadata
	if !data.Metadata.IsNull() {
		var meta map[string]string
		resp.Diagnostics.Append(data.Metadata.ElementsAs(ctx, &meta, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
		params.Metadata = meta
	}

	// MCP servers
	if !data.MCPServers.IsNull() {
		var servers []agentMCPServerModel
		resp.Diagnostics.Append(data.MCPServers.ElementsAs(ctx, &servers, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
		params.MCPServers = make([]anthropic.BetaManagedAgentsURLMCPServerParams, len(servers))
		for i, s := range servers {
			params.MCPServers[i] = anthropic.BetaManagedAgentsURLMCPServerParams{
				Name: s.Name.ValueString(),
				Type: anthropic.BetaManagedAgentsURLMCPServerParamsTypeURL,
				URL:  s.URL.ValueString(),
			}
		}
	}

	// Skills
	resp.Diagnostics.Append(buildSkillsParams(ctx, data.Skills, &params.Skills)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Tools
	tools, diags := buildToolsParams(ctx, data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	params.Tools = tools

	agent, err := r.client.Beta.Agents.New(ctx, params)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create agent: %s", err))
		return
	}

	resp.Diagnostics.Append(mapAgentResponseToState(ctx, agent, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Read ---

func (r *AgentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data AgentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	agent, err := r.client.Beta.Agents.Get(ctx, data.ID.ValueString(), anthropic.BetaAgentGetParams{})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read agent: %s", err))
		return
	}

	// If the agent was archived externally, remove it from state.
	if !agent.ArchivedAt.IsZero() {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(mapAgentResponseToState(ctx, agent, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Update ---

func (r *AgentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data AgentResourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// Read current version from state for optimistic locking.
	var state AgentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	params := anthropic.BetaAgentUpdateParams{
		Version: state.Version.ValueInt64(),
		Model: anthropic.BetaManagedAgentsModelConfigParams{
			ID: anthropic.BetaManagedAgentsModel(data.Model.ValueString()),
		},
		Name: param.NewOpt(data.Name.ValueString()),
	}

	if !data.ModelSpeed.IsNull() && !data.ModelSpeed.IsUnknown() {
		params.Model.Speed = anthropic.BetaManagedAgentsModelConfigParamsSpeed(data.ModelSpeed.ValueString())
	}

	if !data.Description.IsNull() {
		params.Description = param.NewOpt(data.Description.ValueString())
	} else {
		params.Description = param.NewOpt("")
	}

	if !data.System.IsNull() {
		params.System = param.NewOpt(data.System.ValueString())
	} else {
		params.System = param.NewOpt("")
	}

	// Metadata
	if !data.Metadata.IsNull() {
		var meta map[string]string
		resp.Diagnostics.Append(data.Metadata.ElementsAs(ctx, &meta, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
		params.Metadata = meta
	} else {
		params.Metadata = map[string]string{}
	}

	// MCP servers
	if !data.MCPServers.IsNull() {
		var servers []agentMCPServerModel
		resp.Diagnostics.Append(data.MCPServers.ElementsAs(ctx, &servers, false)...)
		if resp.Diagnostics.HasError() {
			return
		}
		params.MCPServers = make([]anthropic.BetaManagedAgentsURLMCPServerParams, len(servers))
		for i, s := range servers {
			params.MCPServers[i] = anthropic.BetaManagedAgentsURLMCPServerParams{
				Name: s.Name.ValueString(),
				Type: anthropic.BetaManagedAgentsURLMCPServerParamsTypeURL,
				URL:  s.URL.ValueString(),
			}
		}
	} else {
		params.MCPServers = []anthropic.BetaManagedAgentsURLMCPServerParams{}
	}

	// Skills — explicitly send empty slice when null to clear API-side skills.
	resp.Diagnostics.Append(buildSkillsParams(ctx, data.Skills, &params.Skills)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if params.Skills == nil {
		params.Skills = []anthropic.BetaManagedAgentsSkillParamsUnion{}
	}

	// Tools
	createTools, diags := buildToolsParams(ctx, data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	// Convert BetaAgentNewParamsToolUnion to BetaAgentUpdateParamsToolUnion
	updateTools := make([]anthropic.BetaAgentUpdateParamsToolUnion, len(createTools))
	for i, t := range createTools {
		updateTools[i] = anthropic.BetaAgentUpdateParamsToolUnion{
			OfAgentToolset20260401: t.OfAgentToolset20260401,
			OfMCPToolset:           t.OfMCPToolset,
			OfCustom:               t.OfCustom,
		}
	}
	params.Tools = updateTools

	agent, err := r.client.Beta.Agents.Update(ctx, state.ID.ValueString(), params)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update agent: %s", err))
		return
	}

	resp.Diagnostics.Append(mapAgentResponseToState(ctx, agent, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// --- Delete (archive) ---

func (r *AgentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var data AgentResourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	_, err := r.client.Beta.Agents.Archive(ctx, data.ID.ValueString(), anthropic.BetaAgentArchiveParams{})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to archive agent: %s", err))
		return
	}
}

// --- ImportState ---

func (r *AgentResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

// ============================================================================
// Helper functions
// ============================================================================

// buildSkillsParams converts Terraform skills list to SDK params.
func buildSkillsParams(ctx context.Context, skillsList types.List, target *[]anthropic.BetaManagedAgentsSkillParamsUnion) diag.Diagnostics {
	var diags diag.Diagnostics

	if skillsList.IsNull() || len(skillsList.Elements()) == 0 {
		return diags
	}

	var skills []agentSkillModel
	diags.Append(skillsList.ElementsAs(ctx, &skills, false)...)
	if diags.HasError() {
		return diags
	}

	result := make([]anthropic.BetaManagedAgentsSkillParamsUnion, len(skills))
	for i, s := range skills {
		switch s.Type.ValueString() {
		case "anthropic":
			sk := anthropic.BetaManagedAgentsAnthropicSkillParams{
				SkillID: s.SkillID.ValueString(),
				Type:    anthropic.BetaManagedAgentsAnthropicSkillParamsTypeAnthropic,
			}
			if !s.Version.IsNull() && !s.Version.IsUnknown() {
				sk.Version = param.NewOpt(s.Version.ValueString())
			}
			result[i] = anthropic.BetaManagedAgentsSkillParamsUnion{OfAnthropic: &sk}
		case "custom":
			sk := anthropic.BetaManagedAgentsCustomSkillParams{
				SkillID: s.SkillID.ValueString(),
				Type:    anthropic.BetaManagedAgentsCustomSkillParamsTypeCustom,
			}
			if !s.Version.IsNull() && !s.Version.IsUnknown() {
				sk.Version = param.NewOpt(s.Version.ValueString())
			}
			result[i] = anthropic.BetaManagedAgentsSkillParamsUnion{OfCustom: &sk}
		}
	}

	*target = result
	return diags
}

// buildToolsParams converts the three tool-related attributes into a single SDK tools slice.
func buildToolsParams(ctx context.Context, data AgentResourceModel) ([]anthropic.BetaAgentNewParamsToolUnion, diag.Diagnostics) {
	var diags diag.Diagnostics
	var tools []anthropic.BetaAgentNewParamsToolUnion

	// Agent toolset
	if !data.AgentToolset.IsNull() && !data.AgentToolset.IsUnknown() {
		var toolset agentToolsetModel
		diags.Append(data.AgentToolset.As(ctx, &toolset, basetypes.ObjectAsOptions{})...)
		if diags.HasError() {
			return nil, diags
		}

		params := anthropic.BetaManagedAgentsAgentToolset20260401Params{
			Type: anthropic.BetaManagedAgentsAgentToolset20260401ParamsTypeAgentToolset20260401,
		}

		if !toolset.DefaultEnabled.IsNull() && !toolset.DefaultEnabled.IsUnknown() {
			params.DefaultConfig.Enabled = param.NewOpt(toolset.DefaultEnabled.ValueBool())
		}
		if !toolset.DefaultPermissionPolicy.IsNull() && !toolset.DefaultPermissionPolicy.IsUnknown() {
			params.DefaultConfig.PermissionPolicy = buildAgentToolsetDefaultPermissionPolicy(toolset.DefaultPermissionPolicy.ValueString())
		}

		if !toolset.Configs.IsNull() && len(toolset.Configs.Elements()) > 0 {
			var configs []agentToolConfigModel
			diags.Append(toolset.Configs.ElementsAs(ctx, &configs, false)...)
			if diags.HasError() {
				return nil, diags
			}
			sdkConfigs := make([]anthropic.BetaManagedAgentsAgentToolConfigParams, len(configs))
			for i, c := range configs {
				sdkConfigs[i] = anthropic.BetaManagedAgentsAgentToolConfigParams{
					Name: anthropic.BetaManagedAgentsAgentToolConfigParamsName(c.Name.ValueString()),
				}
				if !c.Enabled.IsNull() && !c.Enabled.IsUnknown() {
					sdkConfigs[i].Enabled = param.NewOpt(c.Enabled.ValueBool())
				}
				if !c.PermissionPolicy.IsNull() && !c.PermissionPolicy.IsUnknown() {
					sdkConfigs[i].PermissionPolicy = buildAgentToolConfigPermissionPolicy(c.PermissionPolicy.ValueString())
				}
			}
			params.Configs = sdkConfigs
		}

		tools = append(tools, anthropic.BetaAgentNewParamsToolUnion{OfAgentToolset20260401: &params})
	}

	// MCP toolsets
	if !data.MCPToolsets.IsNull() && len(data.MCPToolsets.Elements()) > 0 {
		var toolsets []agentMCPToolsetModel
		diags.Append(data.MCPToolsets.ElementsAs(ctx, &toolsets, false)...)
		if diags.HasError() {
			return nil, diags
		}

		for _, t := range toolsets {
			p := anthropic.BetaManagedAgentsMCPToolsetParams{
				MCPServerName: t.MCPServerName.ValueString(),
				Type:          anthropic.BetaManagedAgentsMCPToolsetParamsTypeMCPToolset,
			}

			if !t.DefaultEnabled.IsNull() && !t.DefaultEnabled.IsUnknown() {
				p.DefaultConfig.Enabled = param.NewOpt(t.DefaultEnabled.ValueBool())
			}
			if !t.DefaultPermissionPolicy.IsNull() && !t.DefaultPermissionPolicy.IsUnknown() {
				p.DefaultConfig.PermissionPolicy = buildMCPToolsetDefaultPermissionPolicy(t.DefaultPermissionPolicy.ValueString())
			}

			if !t.Configs.IsNull() && len(t.Configs.Elements()) > 0 {
				var configs []agentToolConfigModel
				diags.Append(t.Configs.ElementsAs(ctx, &configs, false)...)
				if diags.HasError() {
					return nil, diags
				}
				sdkConfigs := make([]anthropic.BetaManagedAgentsMCPToolConfigParams, len(configs))
				for i, c := range configs {
					sdkConfigs[i] = anthropic.BetaManagedAgentsMCPToolConfigParams{
						Name: c.Name.ValueString(),
					}
					if !c.Enabled.IsNull() && !c.Enabled.IsUnknown() {
						sdkConfigs[i].Enabled = param.NewOpt(c.Enabled.ValueBool())
					}
					if !c.PermissionPolicy.IsNull() && !c.PermissionPolicy.IsUnknown() {
						sdkConfigs[i].PermissionPolicy = buildMCPToolConfigPermissionPolicy(c.PermissionPolicy.ValueString())
					}
				}
				p.Configs = sdkConfigs
			}

			tools = append(tools, anthropic.BetaAgentNewParamsToolUnion{OfMCPToolset: &p})
		}
	}

	// Custom tools
	if !data.CustomTools.IsNull() && len(data.CustomTools.Elements()) > 0 {
		var customTools []agentCustomToolModel
		diags.Append(data.CustomTools.ElementsAs(ctx, &customTools, false)...)
		if diags.HasError() {
			return nil, diags
		}

		for _, t := range customTools {
			p := anthropic.BetaManagedAgentsCustomToolParams{
				Name:        t.Name.ValueString(),
				Description: t.Description.ValueString(),
				Type:        anthropic.BetaManagedAgentsCustomToolParamsTypeCustom,
			}
			if !t.InputSchema.IsNull() && !t.InputSchema.IsUnknown() {
				var schema anthropic.BetaManagedAgentsCustomToolInputSchemaParam
				if err := json.Unmarshal([]byte(t.InputSchema.ValueString()), &schema); err != nil {
					diags.AddError("Invalid input_schema", fmt.Sprintf("Failed to parse input_schema JSON for tool %q: %s", t.Name.ValueString(), err))
					return nil, diags
				}
				p.InputSchema = schema
			}
			tools = append(tools, anthropic.BetaAgentNewParamsToolUnion{OfCustom: &p})
		}
	}

	return tools, diags
}

// mapAgentResponseToState maps the API response to the Terraform state model.
func mapAgentResponseToState(ctx context.Context, agent *anthropic.BetaManagedAgentsAgent, data *AgentResourceModel) diag.Diagnostics {
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

	// Model speed: when API returns "" (meaning "standard"), preserve whatever was
	// already in data (from prior state or plan) to prevent perpetual diffs.
	if agent.Model.Speed != "" {
		data.ModelSpeed = types.StringValue(string(agent.Model.Speed))
	} else if data.ModelSpeed.IsUnknown() || data.ModelSpeed.IsNull() {
		data.ModelSpeed = types.StringNull()
	}
	// else: keep existing data.ModelSpeed (e.g. "standard") to avoid drift

	// Description
	if agent.Description != "" {
		data.Description = types.StringValue(agent.Description)
	} else if !data.Description.IsNull() {
		data.Description = types.StringNull()
	}

	// System
	if agent.System != "" {
		data.System = types.StringValue(agent.System)
	} else if !data.System.IsNull() {
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
	} else if !data.Metadata.IsNull() {
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
	} else if !data.MCPServers.IsNull() {
		data.MCPServers = types.ListNull(types.ObjectType{AttrTypes: agentMCPServerAttrTypes})
	}

	// Skills
	if len(agent.Skills) > 0 {
		skillObjs := make([]attr.Value, len(agent.Skills))
		for i, s := range agent.Skills {
			version := types.StringValue(s.Version)
			obj, d := types.ObjectValue(agentSkillAttrTypes, map[string]attr.Value{
				"type":     types.StringValue(s.Type),
				"skill_id": types.StringValue(s.SkillID),
				"version":  version,
			})
			diags.Append(d...)
			skillObjs[i] = obj
		}
		skillList, d := types.ListValue(types.ObjectType{AttrTypes: agentSkillAttrTypes}, skillObjs)
		diags.Append(d...)
		data.Skills = skillList
	} else if !data.Skills.IsNull() {
		data.Skills = types.ListNull(types.ObjectType{AttrTypes: agentSkillAttrTypes})
	}

	// Tools: split API response into agent_toolset, mcp_toolsets, and custom_tools
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
		toolsetObj, d := mapAgentToolsetToState(ctx, apiAgentToolset, data.AgentToolset)
		diags.Append(d...)
		data.AgentToolset = toolsetObj
	} else if !data.AgentToolset.IsNull() {
		data.AgentToolset = types.ObjectNull(agentToolsetAttrTypes)
	}

	// MCP toolsets
	if len(apiMCPToolsets) > 0 {
		mcpList, d := mapMCPToolsetsToState(ctx, apiMCPToolsets, data.MCPToolsets)
		diags.Append(d...)
		data.MCPToolsets = mcpList
	} else if !data.MCPToolsets.IsNull() {
		data.MCPToolsets = types.ListNull(types.ObjectType{AttrTypes: agentMCPToolsetAttrTypes})
	}

	// Custom tools
	if len(apiCustomTools) > 0 {
		toolObjs := make([]attr.Value, len(apiCustomTools))
		for i, t := range apiCustomTools {
			inputSchema := jsontypes.NewNormalizedNull()
			if raw := t.InputSchema.RawJSON(); raw != "" && raw != "null" {
				inputSchema = jsontypes.NewNormalizedValue(raw)
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
	} else if !data.CustomTools.IsNull() {
		data.CustomTools = types.ListNull(types.ObjectType{AttrTypes: agentCustomToolAttrTypes})
	}

	return diags
}

// mapAgentToolsetToState maps the API agent toolset response to a Terraform object,
// filtering per-tool configs to only those tracked in the current state.
func mapAgentToolsetToState(ctx context.Context, apiToolset *anthropic.BetaManagedAgentsAgentToolset20260401, currentState types.Object) (types.Object, diag.Diagnostics) {
	var diags diag.Diagnostics

	defaultEnabled := types.BoolValue(apiToolset.DefaultConfig.Enabled)
	defaultPermPolicy := types.StringValue(apiToolset.DefaultConfig.PermissionPolicy.Type)

	// Determine which configs to include in state.
	configsList := types.ListNull(types.ObjectType{AttrTypes: agentToolConfigAttrTypes})

	if !currentState.IsNull() && !currentState.IsUnknown() {
		var stateToolset agentToolsetModel
		diags.Append(currentState.As(ctx, &stateToolset, basetypes.ObjectAsOptions{})...)
		if diags.HasError() {
			return types.ObjectNull(agentToolsetAttrTypes), diags
		}

		if !stateToolset.Configs.IsNull() {
			var stateConfigs []agentToolConfigModel
			diags.Append(stateToolset.Configs.ElementsAs(ctx, &stateConfigs, false)...)
			if diags.HasError() {
				return types.ObjectNull(agentToolsetAttrTypes), diags
			}

			// Build lookup from API response.
			apiByName := map[string]anthropic.BetaManagedAgentsAgentToolConfig{}
			for _, c := range apiToolset.Configs {
				apiByName[string(c.Name)] = c
			}

			// Keep only configs matching state names, in state order.
			configObjs := make([]attr.Value, 0, len(stateConfigs))
			for _, sc := range stateConfigs {
				name := sc.Name.ValueString()
				if apiCfg, ok := apiByName[name]; ok {
					enabled := types.BoolValue(apiCfg.Enabled)
					permPolicy := types.StringValue(apiCfg.PermissionPolicy.Type)
					// Preserve null for fields the user didn't specify.
					if sc.Enabled.IsNull() {
						enabled = types.BoolNull()
					}
					if sc.PermissionPolicy.IsNull() {
						permPolicy = types.StringNull()
					}
					obj, d := types.ObjectValue(agentToolConfigAttrTypes, map[string]attr.Value{
						"name":              types.StringValue(name),
						"enabled":           enabled,
						"permission_policy": permPolicy,
					})
					diags.Append(d...)
					configObjs = append(configObjs, obj)
				}
			}

			list, d := types.ListValue(types.ObjectType{AttrTypes: agentToolConfigAttrTypes}, configObjs)
			diags.Append(d...)
			configsList = list
		}
	} else if len(apiToolset.Configs) > 0 {
		// Import case (null state): populate all API configs.
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

// mapMCPToolsetsToState maps API MCP toolsets to a Terraform list, matching by server name
// and filtering per-tool configs to only those tracked in current state.
func mapMCPToolsetsToState(ctx context.Context, apiToolsets []anthropic.BetaManagedAgentsMCPToolset, currentState types.List) (types.List, diag.Diagnostics) {
	var diags diag.Diagnostics

	apiByServer := map[string]anthropic.BetaManagedAgentsMCPToolset{}
	for _, t := range apiToolsets {
		apiByServer[t.MCPServerName] = t
	}

	// Extract current state toolsets to preserve order and config filtering.
	var stateToolsets []agentMCPToolsetModel
	if !currentState.IsNull() {
		diags.Append(currentState.ElementsAs(ctx, &stateToolsets, false)...)
		if diags.HasError() {
			return types.ListNull(types.ObjectType{AttrTypes: agentMCPToolsetAttrTypes}), diags
		}
	}

	toolsetObjs := make([]attr.Value, 0)

	if len(stateToolsets) > 0 {
		// Normal Read: iterate state toolsets to preserve order and filter configs.
		for _, st := range stateToolsets {
			serverName := st.MCPServerName.ValueString()
			apiToolset, exists := apiByServer[serverName]
			if !exists {
				continue
			}

			defaultEnabled := types.BoolValue(apiToolset.DefaultConfig.Enabled)
			defaultPermPolicy := types.StringValue(apiToolset.DefaultConfig.PermissionPolicy.Type)

			configsList := types.ListNull(types.ObjectType{AttrTypes: agentMCPToolConfigAttrTypes})

			if !st.Configs.IsNull() {
				var stateConfigs []agentToolConfigModel
				diags.Append(st.Configs.ElementsAs(ctx, &stateConfigs, false)...)
				if diags.HasError() {
					return types.ListNull(types.ObjectType{AttrTypes: agentMCPToolsetAttrTypes}), diags
				}

				apiCfgByName := map[string]anthropic.BetaManagedAgentsMCPToolConfig{}
				for _, c := range apiToolset.Configs {
					apiCfgByName[c.Name] = c
				}

				configObjs := make([]attr.Value, 0, len(stateConfigs))
				for _, sc := range stateConfigs {
					name := sc.Name.ValueString()
					if apiCfg, ok := apiCfgByName[name]; ok {
						enabled := types.BoolValue(apiCfg.Enabled)
						permPolicy := types.StringValue(apiCfg.PermissionPolicy.Type)
						if sc.Enabled.IsNull() {
							enabled = types.BoolNull()
						}
						if sc.PermissionPolicy.IsNull() {
							permPolicy = types.StringNull()
						}
						obj, d := types.ObjectValue(agentMCPToolConfigAttrTypes, map[string]attr.Value{
							"name":              types.StringValue(name),
							"enabled":           enabled,
							"permission_policy": permPolicy,
						})
						diags.Append(d...)
						configObjs = append(configObjs, obj)
					}
				}

				list, d := types.ListValue(types.ObjectType{AttrTypes: agentMCPToolConfigAttrTypes}, configObjs)
				diags.Append(d...)
				configsList = list
			}

			obj, d := types.ObjectValue(agentMCPToolsetAttrTypes, map[string]attr.Value{
				"mcp_server_name":           types.StringValue(serverName),
				"default_enabled":           defaultEnabled,
				"default_permission_policy": defaultPermPolicy,
				"configs":                   configsList,
			})
			diags.Append(d...)
			toolsetObjs = append(toolsetObjs, obj)
		}
	} else {
		// Import case (null/empty state): populate all API toolsets with all configs.
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
	}

	list, d := types.ListValue(types.ObjectType{AttrTypes: agentMCPToolsetAttrTypes}, toolsetObjs)
	diags.Append(d...)
	return list, diags
}

// --- Permission policy builder helpers ---

func buildAgentToolConfigPermissionPolicy(policyType string) anthropic.BetaManagedAgentsAgentToolConfigParamsPermissionPolicyUnion {
	switch policyType {
	case "always_allow":
		return anthropic.BetaManagedAgentsAgentToolConfigParamsPermissionPolicyUnion{
			OfAlwaysAllow: &anthropic.BetaManagedAgentsAlwaysAllowPolicyParam{
				Type: anthropic.BetaManagedAgentsAlwaysAllowPolicyTypeAlwaysAllow,
			},
		}
	case "always_ask":
		return anthropic.BetaManagedAgentsAgentToolConfigParamsPermissionPolicyUnion{
			OfAlwaysAsk: &anthropic.BetaManagedAgentsAlwaysAskPolicyParam{
				Type: anthropic.BetaManagedAgentsAlwaysAskPolicyTypeAlwaysAsk,
			},
		}
	}
	return anthropic.BetaManagedAgentsAgentToolConfigParamsPermissionPolicyUnion{}
}

func buildAgentToolsetDefaultPermissionPolicy(policyType string) anthropic.BetaManagedAgentsAgentToolsetDefaultConfigParamsPermissionPolicyUnion {
	switch policyType {
	case "always_allow":
		return anthropic.BetaManagedAgentsAgentToolsetDefaultConfigParamsPermissionPolicyUnion{
			OfAlwaysAllow: &anthropic.BetaManagedAgentsAlwaysAllowPolicyParam{
				Type: anthropic.BetaManagedAgentsAlwaysAllowPolicyTypeAlwaysAllow,
			},
		}
	case "always_ask":
		return anthropic.BetaManagedAgentsAgentToolsetDefaultConfigParamsPermissionPolicyUnion{
			OfAlwaysAsk: &anthropic.BetaManagedAgentsAlwaysAskPolicyParam{
				Type: anthropic.BetaManagedAgentsAlwaysAskPolicyTypeAlwaysAsk,
			},
		}
	}
	return anthropic.BetaManagedAgentsAgentToolsetDefaultConfigParamsPermissionPolicyUnion{}
}

func buildMCPToolConfigPermissionPolicy(policyType string) anthropic.BetaManagedAgentsMCPToolConfigParamsPermissionPolicyUnion {
	switch policyType {
	case "always_allow":
		return anthropic.BetaManagedAgentsMCPToolConfigParamsPermissionPolicyUnion{
			OfAlwaysAllow: &anthropic.BetaManagedAgentsAlwaysAllowPolicyParam{
				Type: anthropic.BetaManagedAgentsAlwaysAllowPolicyTypeAlwaysAllow,
			},
		}
	case "always_ask":
		return anthropic.BetaManagedAgentsMCPToolConfigParamsPermissionPolicyUnion{
			OfAlwaysAsk: &anthropic.BetaManagedAgentsAlwaysAskPolicyParam{
				Type: anthropic.BetaManagedAgentsAlwaysAskPolicyTypeAlwaysAsk,
			},
		}
	}
	return anthropic.BetaManagedAgentsMCPToolConfigParamsPermissionPolicyUnion{}
}

func buildMCPToolsetDefaultPermissionPolicy(policyType string) anthropic.BetaManagedAgentsMCPToolsetDefaultConfigParamsPermissionPolicyUnion {
	switch policyType {
	case "always_allow":
		return anthropic.BetaManagedAgentsMCPToolsetDefaultConfigParamsPermissionPolicyUnion{
			OfAlwaysAllow: &anthropic.BetaManagedAgentsAlwaysAllowPolicyParam{
				Type: anthropic.BetaManagedAgentsAlwaysAllowPolicyTypeAlwaysAllow,
			},
		}
	case "always_ask":
		return anthropic.BetaManagedAgentsMCPToolsetDefaultConfigParamsPermissionPolicyUnion{
			OfAlwaysAsk: &anthropic.BetaManagedAgentsAlwaysAskPolicyParam{
				Type: anthropic.BetaManagedAgentsAlwaysAskPolicyTypeAlwaysAsk,
			},
		}
	}
	return anthropic.BetaManagedAgentsMCPToolsetDefaultConfigParamsPermissionPolicyUnion{}
}
