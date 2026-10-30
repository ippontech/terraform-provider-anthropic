// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents

import (
	"context"
	"fmt"
	"maps"
	"math"
	"regexp"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/hashicorp/terraform-plugin-framework-jsontypes/jsontypes"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
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
	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &AgentResource{}
var _ resource.ResourceWithImportState = &AgentResource{}
var _ resource.ResourceWithConfigValidators = &AgentResource{}

func NewAgentResource() resource.Resource {
	return &AgentResource{}
}

// AgentResource defines the resource implementation.
type AgentResource struct {
	client *anthropic.Client
}

// --- Terraform data models ---

type AgentResourceModel struct {
	Model             types.String `tfsdk:"model"`
	Name              types.String `tfsdk:"name"`
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
	ID                types.String `tfsdk:"id"`
	Version           types.Int64  `tfsdk:"version"`
	CreatedAt         types.String `tfsdk:"created_at"`
	UpdatedAt         types.String `tfsdk:"updated_at"`
	ArchivedAt        types.String `tfsdk:"archived_at"`
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

// agentToolConfigModel is an `agent_toolset.configs` entry. The web options
// apply to web_fetch and web_search only (agent_web_tools.go).
type agentToolConfigModel struct {
	Name             types.String `tfsdk:"name"`
	Enabled          types.Bool   `tfsdk:"enabled"`
	PermissionPolicy types.String `tfsdk:"permission_policy"`
	AllowedDomains   types.Set    `tfsdk:"allowed_domains"`
	BlockedDomains   types.Set    `tfsdk:"blocked_domains"`
	MaxContentTokens types.Int64  `tfsdk:"max_content_tokens"`
	UserLocation     types.Object `tfsdk:"user_location"`
	URLSources       types.Object `tfsdk:"url_sources"`
}

// agentMCPToolConfigModel is an `mcp_toolsets[*].configs` entry.
type agentMCPToolConfigModel struct {
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
	"name":               types.StringType,
	"enabled":            types.BoolType,
	"permission_policy":  types.StringType,
	"allowed_domains":    types.SetType{ElemType: types.StringType},
	"blocked_domains":    types.SetType{ElemType: types.StringType},
	"max_content_tokens": types.Int64Type,
	"user_location":      types.ObjectType{AttrTypes: agentUserLocationAttrTypes},
	"url_sources":        types.ObjectType{AttrTypes: agentURLSourcesAttrTypes},
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

// Permission policy values
// (https://platform.claude.com/docs/en/managed-agents/permission-policies).
const (
	policyAlwaysAllow = "always_allow"
	policyAlwaysAsk   = "always_ask"
	policyAuto        = "auto"
)

var permissionPolicyValidator = stringvalidator.OneOf(policyAlwaysAllow, policyAlwaysAsk, policyAuto)

// permissionPolicyValuesDescription describes the values of a toolset's
// default permission policy; per-tool overrides list the values only.
const permissionPolicyValuesDescription = "`always_allow` (runs without confirmation), `always_ask` (waits for approval) or `auto` " +
	"(the server evaluates each call and runs it, denies it or waits for approval; not a human checkpoint, since a call it judges safe runs before anyone sees it)"

// uniqueMCPServerNamesValidator rejects two mcp_toolsets entries for the same
// server: mapMCPToolsetsToState matches toolsets by server name and keeps one
// entry per server, so a second entry would make the state shorter than the
// plan. Unknown entries and names are skipped, so they stay valid at plan time.
type uniqueMCPServerNamesValidator struct{}

func (uniqueMCPServerNamesValidator) Description(context.Context) string {
	return "each mcp_server_name appears in at most one entry"
}

func (uniqueMCPServerNamesValidator) MarkdownDescription(context.Context) string {
	return "each `mcp_server_name` appears in at most one entry"
}

func (uniqueMCPServerNamesValidator) ValidateList(_ context.Context, req validator.ListRequest, resp *validator.ListResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	firstIndex := map[string]int{}
	for i, el := range req.ConfigValue.Elements() {
		obj, ok := el.(types.Object)
		if !ok || obj.IsNull() || obj.IsUnknown() {
			continue
		}
		name, ok := obj.Attributes()["mcp_server_name"].(types.String)
		if !ok || name.IsNull() || name.IsUnknown() {
			continue
		}
		if first, seen := firstIndex[name.ValueString()]; seen {
			resp.Diagnostics.AddAttributeError(req.Path.AtListIndex(i).AtName("mcp_server_name"), "Duplicate MCP toolset",
				fmt.Sprintf("Each MCP server takes at most one mcp_toolsets entry, but %q is set in entries %d and %d. Merge them into one entry.",
					name.ValueString(), first, i))
			continue
		}
		firstIndex[name.ValueString()] = i
	}
}

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
			"model_effort": schema.StringAttribute{
				Optional: true,
				Computed: true,
				MarkdownDescription: "How hard Claude works on each turn: `low`, `medium`, `high`, `xhigh` or `max`. " +
					"`xhigh` and `max` spend noticeably more tokens, so they are cost-relevant. " +
					"When omitted on create, the API resolves a per-model default and it is stored here. " +
					"Effort follows the model: changing `model` without setting `model_effort` resolves the new model's default (known after apply). " +
					"With an unchanged `model`, removing the attribute from the configuration keeps the last value rather than resetting it to the default.",
				Validators:    []validator.String{stringvalidator.OneOf("low", "medium", "high", "xhigh", "max")},
				PlanModifiers: []planmodifier.String{modelEffortFollowsModel()},
			},
			"model_inference_geo": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "Geographic region that serves the agent's model requests (for example `us` or `global`). " +
					"The value is not validated locally because the data residency documentation does not state that these are the only values. " +
					"When unset, requests follow the workspace's default inference geo. " +
					"Removing the attribute clears the pin on the next update.",
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
				MarkdownDescription: "Configuration for built-in agent tools (" + strings.Join(builtinToolNames(), ", ") + ").",
				Attributes: map[string]schema.Attribute{
					"default_enabled": schema.BoolAttribute{
						Optional:            true,
						Computed:            true,
						MarkdownDescription: "Whether tools are enabled by default. Defaults to `true`.",
					},
					"default_permission_policy": schema.StringAttribute{
						Optional: true,
						Computed: true,
						MarkdownDescription: "Default permission policy: " + permissionPolicyValuesDescription + ". The API applies `always_allow` when omitted. " +
							"Removing the attribute does not plan a change on its own: set the policy you want instead.",
						Validators: []validator.String{permissionPolicyValidator},
					},
					"configs": schema.ListNestedAttribute{
						Optional: true,
						MarkdownDescription: "Per-tool configuration overrides. The `web_fetch` and `web_search` entries also accept " +
							"`allowed_domains` or `blocked_domains`, plus `max_content_tokens` and `url_sources` (`web_fetch`) or `user_location` (`web_search`); " +
							"setting one of these on another tool fails at plan time.",
						NestedObject: schema.NestedAttributeObject{
							Attributes: map[string]schema.Attribute{
								"name": schema.StringAttribute{
									Required:            true,
									MarkdownDescription: "Built-in tool name.",
									Validators:          []validator.String{stringvalidator.OneOf(builtinToolNames()...)},
								},
								"enabled": schema.BoolAttribute{
									Optional:            true,
									MarkdownDescription: "Whether this tool is enabled. Overrides `default_enabled`.",
								},
								"permission_policy": schema.StringAttribute{
									Optional:            true,
									MarkdownDescription: "Permission policy override: `always_allow`, `always_ask` or `auto`.",
									Validators:          []validator.String{permissionPolicyValidator},
								},
								"allowed_domains": schema.SetAttribute{
									Optional:    true,
									ElementType: types.StringType,
									MarkdownDescription: "`web_fetch` and `web_search` only. The only hosts the tool can reach. " +
										"Each entry is a plain hostname such as `docs.example.com` (no scheme, port or wildcard) and also covers its subdomains; " +
										"a `web_search` entry may carry a path suffix. 1-64 entries of 1-255 characters. Conflicts with `blocked_domains`. " +
										"With `limited` environment networking, every entry must also be within the environment's `allowed_hosts`, or creating a session fails.",
									Validators: []validator.Set{
										setvalidator.SizeBetween(1, 64),
										setvalidator.ValueStringsAre(stringvalidator.LengthBetween(1, 255)),
									},
								},
								"blocked_domains": schema.SetAttribute{
									Optional:    true,
									ElementType: types.StringType,
									MarkdownDescription: "`web_fetch` and `web_search` only. Hosts the tool can never reach, written like `allowed_domains`. " +
										"1-64 entries of 1-255 characters. Conflicts with `allowed_domains`.",
									Validators: []validator.Set{
										setvalidator.SizeBetween(1, 64),
										setvalidator.ValueStringsAre(stringvalidator.LengthBetween(1, 255)),
									},
								},
								"max_content_tokens": schema.Int64Attribute{
									Optional: true,
									MarkdownDescription: "`web_fetch` only. Maximum number of tokens of fetched text content included in the context per call. " +
										"Does not apply to binary content such as PDFs. Positive integer.",
									Validators: []validator.Int64{int64validator.Between(1, math.MaxInt32)},
								},
								"user_location": schema.SingleNestedAttribute{
									Optional: true,
									MarkdownDescription: "`web_search` only. Approximate user location used to localize search results " +
										"(sent with `type = \"approximate\"`, the only precision the API supports). At least one field must be set.",
									Attributes: map[string]schema.Attribute{
										"country": schema.StringAttribute{
											Optional:            true,
											MarkdownDescription: "Two-letter ISO 3166-1 country code, uppercase (e.g. `US`). The API rejects a country its search provider does not support.",
											Validators: []validator.String{stringvalidator.RegexMatches(
												regexp.MustCompile(`^[A-Z]{2}$`), "must be a two-letter uppercase ISO 3166-1 country code",
											)},
										},
										"city": schema.StringAttribute{
											Optional:            true,
											MarkdownDescription: "City name. 1-255 characters.",
											Validators:          []validator.String{stringvalidator.LengthBetween(1, 255)},
										},
										"region": schema.StringAttribute{
											Optional:            true,
											MarkdownDescription: "Region or state name. 1-255 characters.",
											Validators:          []validator.String{stringvalidator.LengthBetween(1, 255)},
										},
										"timezone": schema.StringAttribute{
											Optional:            true,
											MarkdownDescription: "IANA timezone identifier (e.g. `America/Los_Angeles`). The API rejects a name that is not a valid IANA timezone.",
											Validators:          []validator.String{stringvalidator.LengthAtLeast(1)},
										},
									},
								},
								"url_sources": urlSourcesSchema(),
							},
						},
					},
				},
			},
			"mcp_toolsets": schema.ListNestedAttribute{
				Optional:            true,
				MarkdownDescription: "Tool configurations for MCP servers. Each entry corresponds to a server defined in `mcp_servers`, at most one entry per server.",
				Validators:          []validator.List{uniqueMCPServerNamesValidator{}},
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
							Optional: true,
							Computed: true,
							MarkdownDescription: "Default permission policy: " + permissionPolicyValuesDescription + ". " +
								"The API applies `always_ask` when omitted, so tools added to the server later do not run without approval. " +
								"Removing the attribute does not plan a change on its own: set `always_ask` to go back to it.",
							Validators: []validator.String{permissionPolicyValidator},
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
										MarkdownDescription: "Permission policy override: `always_allow`, `always_ask` or `auto`.",
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
							Optional:   true,
							CustomType: jsontypes.NormalizedType{},
							MarkdownDescription: "JSON Schema for the tool's input parameters. Use `jsonencode()` to build the value. " +
								"Sent as configured, root-level keywords such as `additionalProperties` and `$defs` included; `type` defaults to `object` when omitted. " +
								"When `input_schema` itself is omitted, `{\"type\": \"object\"}` is sent (a tool without parameters), since the API requires an input schema.",
						},
					},
				},
			},

			"multiagent": schema.SingleNestedAttribute{
				Optional: true,
				MarkdownDescription: "Multiagent orchestration (beta), of type `coordinator` or `multiagent_20261001` (see the Multiagent orchestration section). " +
					"Referenced agents must not have their own `multiagent` (depth limit 1), and `inference_geo` must match across the agent and every agent it references. " +
					"Agent references are snapshotted on create/update: to pick up a newer version of a referenced agent, change its `version`. " +
					"The API resolves a `self` entry into an `agent` reference carrying the agent's own ID (and echoes a coordinator `advisor` entry last); " +
					"state keeps the configured order and `self` entries as configured. " +
					"When imported (no configuration to correlate with), a `self` entry is therefore read back as an `agent` entry whose `id` is the agent's own ID.",
				Attributes: map[string]schema.Attribute{
					"type": schema.StringAttribute{
						Required: true,
						MarkdownDescription: "`coordinator` (a roster in `agents`) or `multiagent_20261001` " +
							"(`subagents`, `workflows` and `advisor`, each turned on or off on its own).",
						Validators: []validator.String{stringvalidator.OneOf(multiagentTypeCoordinator, multiagentType20261001)},
					},
					"subagents": multiagentMemberSchema("subagents",
						"`multiagent_20261001` only. Delegation to subagents, each running in its own session thread (one level deep, at most 25 child threads at a time). "+
							"Omitted, it takes the API default: enabled, with inline agents."),
					"workflows": multiagentMemberSchema("workflows",
						"`multiagent_20261001` only. Dynamic workflows: programs the agent writes, which the server runs in the background with many agents. "+
							"Omitted, it takes the API default: enabled, with inline agents."),
					"advisor": schema.SingleNestedAttribute{
						Optional: true,
						MarkdownDescription: "`multiagent_20261001` only. An advisor model the session's primary thread can consult mid-turn. " +
							"Omitted, the agent has no advisor (the API default). For `coordinator`, use an `advisor` entry in `agents` instead.",
						Attributes: map[string]schema.Attribute{
							"model": schema.StringAttribute{
								Required: true,
								MarkdownDescription: "Advisor model ID. It must be at least as capable as the agent's model, " +
									"or the API rejects the pairing.",
							},
						},
					},
					"agents": schema.ListNestedAttribute{
						Optional: true,
						MarkdownDescription: "`coordinator` only, and required there. Roster of agents the coordinator may spawn. " +
							"1-20 entries referencing distinct agents, at most one `self` and one `advisor`.",
						Validators: []validator.List{listvalidator.SizeBetween(1, 20)},
						NestedObject: schema.NestedAttributeObject{
							Attributes: map[string]schema.Attribute{
								"type": schema.StringAttribute{
									Required:            true,
									MarkdownDescription: "Entry type: `agent` (another agent), `self` (recursive self-invocation) or `advisor` (a model the coordinator may consult mid-turn).",
									Validators:          []validator.String{stringvalidator.OneOf("agent", "self", "advisor")},
								},
								"id": schema.StringAttribute{
									Optional:            true,
									MarkdownDescription: "ID of the referenced agent. Required when `type` is `agent`, forbidden otherwise.",
								},
								"version": schema.Int64Attribute{
									Optional:            true,
									Computed:            true,
									MarkdownDescription: "Pinned version of the referenced agent (`agent` entries only). The API pins the latest version when omitted and echoes it back.",
								},
								"model": schema.StringAttribute{
									Optional:            true,
									MarkdownDescription: "Advisor model ID. Required when `type` is `advisor`, forbidden otherwise.",
								},
							},
						},
					},
				},
			},

			"execution_identity": executionIdentitySchema(),

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

func (r *AgentResource) ConfigValidators(_ context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{&multiagentConfigValidator{}, &agentToolConfigsValidator{}, &executionIdentityConfigValidator{}}
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

	params, diags := buildCreateParams(ctx, data)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	agent, err := r.client.Beta.Agents.New(ctx, params)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to create agent: %s", err))
		return
	}

	resp.Diagnostics.Append(mapAgentResponseToState(ctx, agent, &data, applyMapping)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// buildCreateParams builds the create request from the plan. Unset optional
// attributes are omitted, so the API applies its defaults.
func buildCreateParams(ctx context.Context, data AgentResourceModel) (anthropic.BetaAgentNewParams, diag.Diagnostics) {
	params := anthropic.BetaAgentNewParams{
		Model: buildModelConfigParams(data),
		Name:  data.Name.ValueString(),
	}
	if !data.Description.IsNull() {
		params.Description = param.NewOpt(data.Description.ValueString())
	}
	if !data.System.IsNull() {
		params.System = param.NewOpt(data.System.ValueString())
	}

	var diags diag.Diagnostics
	if !data.Metadata.IsNull() {
		var meta map[string]string
		diags.Append(data.Metadata.ElementsAs(ctx, &meta, false)...)
		if diags.HasError() {
			return params, diags
		}
		params.Metadata = meta
	}

	servers, d := buildMCPServersParams(ctx, data.MCPServers)
	diags.Append(d...)
	if diags.HasError() {
		return params, diags
	}
	params.MCPServers = servers

	diags.Append(buildSkillsParams(ctx, data.Skills, &params.Skills)...)
	if diags.HasError() {
		return params, diags
	}

	tools, d := buildToolsParams(ctx, data)
	diags.Append(d...)
	if diags.HasError() {
		return params, diags
	}
	params.Tools = tools

	multiagent, d := buildMultiagentParams(ctx, data.Multiagent, types.ObjectNull(agentMultiagentAttrTypes))
	diags.Append(d...)
	if diags.HasError() {
		return params, diags
	}
	params.Multiagent = multiagent

	// Execution identity: omitted when unset, which stores the default.
	identity, sendIdentity, d := buildExecutionIdentityParams(ctx, data.ExecutionIdentity, types.ObjectNull(agentExecutionIdentityAttrTypes))
	diags.Append(d...)
	if sendIdentity {
		params.ExecutionIdentity = identity
	}
	return params, diags
}

// buildMCPServersParams builds the mcp_servers of a request, nil when the
// attribute is null.
func buildMCPServersParams(ctx context.Context, list types.List) ([]anthropic.BetaManagedAgentsURLMCPServerParams, diag.Diagnostics) {
	if list.IsNull() {
		return nil, nil
	}
	var servers []agentMCPServerModel
	diags := list.ElementsAs(ctx, &servers, false)
	if diags.HasError() {
		return nil, diags
	}
	params := make([]anthropic.BetaManagedAgentsURLMCPServerParams, len(servers))
	for i, s := range servers {
		params[i] = anthropic.BetaManagedAgentsURLMCPServerParams{
			Name: s.Name.ValueString(),
			Type: anthropic.BetaManagedAgentsURLMCPServerParamsTypeURL,
			URL:  s.URL.ValueString(),
		}
	}
	return params, diags
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
		// Deleted out of band: drop it from state so the next plan re-creates it.
		if providerrors.IsNotFound(err) {
			resp.State.RemoveResource(ctx)
			return
		}
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to read agent: %s", err))
		return
	}

	// If the agent was archived externally, remove it from state.
	if !agent.ArchivedAt.IsZero() {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(mapAgentResponseToState(ctx, agent, &data, readMapping)...)
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

	params, diags := buildUpdateParams(ctx, data, state)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}

	agent, err := r.client.Beta.Agents.Update(ctx, state.ID.ValueString(), params)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Sprintf("Unable to update agent: %s", err))
		return
	}

	resp.Diagnostics.Append(mapAgentResponseToState(ctx, agent, &data, applyMapping)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

// buildUpdateParams builds the update request from the plan and the prior
// state. Every field the configuration can clear is sent, empty when unset,
// since the API keeps an omitted field
// (https://platform.claude.com/docs/en/api/beta/agents/update).
func buildUpdateParams(ctx context.Context, data, state AgentResourceModel) (anthropic.BetaAgentUpdateParams, diag.Diagnostics) {
	params := anthropic.BetaAgentUpdateParams{
		Model:       buildModelConfigParams(data),
		Name:        param.NewOpt(data.Name.ValueString()),
		Description: param.NewOpt(data.Description.ValueString()),
		System:      param.NewOpt(data.System.ValueString()),
	}

	// Optimistic locking: the API requires a version of at least 1 when supplied,
	// and applies the update unconditionally when omitted.
	if v := state.Version.ValueInt64(); v > 0 {
		params.Version = param.NewOpt(v)
	}

	// Metadata is a patch on update: an omitted key is kept and a null one is
	// deleted (https://platform.claude.com/docs/en/api/beta/agents/update), so
	// keys removed from the configuration are sent as null. The typed
	// map[string]string cannot carry a null, hence the extra field.
	metaPatch, diags := tfvalue.MetadataPatch(ctx, data.Metadata, state.Metadata)
	if diags.HasError() {
		return params, diags
	}

	servers, d := buildMCPServersParams(ctx, data.MCPServers)
	diags.Append(d...)
	if diags.HasError() {
		return params, diags
	}
	params.MCPServers = servers
	if params.MCPServers == nil {
		params.MCPServers = []anthropic.BetaManagedAgentsURLMCPServerParams{}
	}

	// Skills — explicitly send empty slice when null to clear API-side skills.
	diags.Append(buildSkillsParams(ctx, data.Skills, &params.Skills)...)
	if diags.HasError() {
		return params, diags
	}
	if params.Skills == nil {
		params.Skills = []anthropic.BetaManagedAgentsSkillParamsUnion{}
	}

	tools, d := buildToolsParams(ctx, data)
	diags.Append(d...)
	if diags.HasError() {
		return params, diags
	}
	params.Tools = updateToolsParams(tools)

	// Multiagent: clearing must send an explicit null, an omitted field keeps the roster.
	multiagent, d := buildMultiagentParams(ctx, data.Multiagent, state.Multiagent)
	diags.Append(d...)
	if diags.HasError() {
		return params, diags
	}
	params.Multiagent = multiagent

	// SetExtraFields replaces the extra fields, so it is called once.
	if extraFields := updateExtraFields(metaPatch, data, state); len(extraFields) > 0 {
		params.SetExtraFields(extraFields)
	}

	// Execution identity: removing it sends the service account (the default).
	identity, sendIdentity, d := buildExecutionIdentityParams(ctx, data.ExecutionIdentity, state.ExecutionIdentity)
	diags.Append(d...)
	if sendIdentity {
		params.ExecutionIdentity = identity
	}
	return params, diags
}

// updateToolsParams converts the create tools union into the update one; it
// is never nil, so an update without tools sends [] and clears them.
func updateToolsParams(tools []anthropic.BetaAgentNewParamsToolUnion) []anthropic.BetaAgentUpdateParamsToolUnion {
	updateTools := make([]anthropic.BetaAgentUpdateParamsToolUnion, len(tools))
	for i, t := range tools {
		updateTools[i] = anthropic.BetaAgentUpdateParamsToolUnion{
			OfAgentToolset20260401: t.OfAgentToolset20260401,
			OfMCPToolset:           t.OfMCPToolset,
			OfCustom:               t.OfCustom,
		}
	}
	return updateTools
}

// updateExtraFields returns the members of the update the typed params cannot
// carry: the metadata patch, whose deleted keys are JSON nulls, and
// multiagent: null when the configuration removes it (see
// multiagentClearField).
func updateExtraFields(metaPatch map[string]any, data, state AgentResourceModel) map[string]any {
	extraFields := map[string]any{}
	if len(metaPatch) > 0 {
		extraFields["metadata"] = metaPatch
	}
	if data.Multiagent.IsNull() && !state.Multiagent.IsNull() {
		maps.Copy(extraFields, multiagentClearField())
	}
	return extraFields
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
		if providerrors.IsNotFound(err) {
			return // already gone
		}
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

// buildModelConfigParams builds the model object sent on create and update. On
// update the API replaces `model` as a whole: omitting inference_geo clears it,
// and omitting effort keeps the stored value only when the model id is unchanged
// (otherwise it resolves the new model's default). Effort is therefore sent only
// when the plan knows it (the model_effort plan modifier makes it unknown when
// `model` changes); a null planned inference_geo is deliberately omitted so that
// removing it from the configuration clears the pin.
func buildModelConfigParams(plan AgentResourceModel) anthropic.BetaManagedAgentsModelConfigParams {
	model := anthropic.BetaManagedAgentsModelConfigParams{
		ID: anthropic.BetaManagedAgentsModel(plan.Model.ValueString()),
	}
	if !plan.ModelSpeed.IsNull() && !plan.ModelSpeed.IsUnknown() {
		model.Speed = anthropic.BetaManagedAgentsModelConfigParamsSpeed(plan.ModelSpeed.ValueString())
	}

	if !plan.ModelEffort.IsNull() && !plan.ModelEffort.IsUnknown() {
		model.Effort = anthropic.BetaManagedAgentsModelConfigParamsEffortUnion{
			OfBetaManagedAgentsModelConfigsEffortBetaManagedAgentsEffortLevel: param.NewOpt(plan.ModelEffort.ValueString()),
		}
	}

	if !plan.ModelInferenceGeo.IsNull() && !plan.ModelInferenceGeo.IsUnknown() {
		model.InferenceGeo = param.NewOpt(plan.ModelInferenceGeo.ValueString())
	}
	return model
}

// modelEffortFromResponse returns the effort level of an API model config, or
// null when the field is absent or empty.
func modelEffortFromResponse(m anthropic.BetaManagedAgentsModelConfig) types.String {
	if m.JSON.Effort.Valid() && m.Effort.Type != "" {
		return types.StringValue(m.Effort.Type)
	}
	return types.StringNull()
}

// modelInferenceGeoFromResponse returns the inference geo of an API model
// config, or null when the field is absent or empty.
func modelInferenceGeoFromResponse(m anthropic.BetaManagedAgentsModelConfig) types.String {
	if m.JSON.InferenceGeo.Valid() && m.InferenceGeo != "" {
		return types.StringValue(m.InferenceGeo)
	}
	return types.StringNull()
}

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

// isKnown reports whether v holds a value: neither null nor unknown.
func isKnown(v attr.Value) bool {
	return !v.IsNull() && !v.IsUnknown()
}

// buildToolsParams converts the three tool-related attributes into a single
// SDK tools slice: the agent toolset, then the MCP toolsets, then the custom
// tools. It is nil when none is configured.
func buildToolsParams(ctx context.Context, data AgentResourceModel) ([]anthropic.BetaAgentNewParamsToolUnion, diag.Diagnostics) {
	var tools []anthropic.BetaAgentNewParamsToolUnion

	toolset, diags := buildAgentToolsetParams(ctx, data.AgentToolset)
	if diags.HasError() {
		return nil, diags
	}
	if toolset != nil {
		tools = append(tools, anthropic.BetaAgentNewParamsToolUnion{OfAgentToolset20260401: toolset})
	}

	mcpToolsets, d := buildMCPToolsetsParams(ctx, data.MCPToolsets)
	diags.Append(d...)
	if diags.HasError() {
		return nil, diags
	}
	tools = append(tools, mcpToolsets...)

	customTools, d := buildCustomToolsParams(ctx, data.CustomTools)
	diags.Append(d...)
	if diags.HasError() {
		return nil, diags
	}
	return append(tools, customTools...), diags
}

// buildAgentToolsetParams builds the agent_toolset_20260401 tool, nil when
// agent_toolset is null or unknown. Defaults left unset are omitted, so the
// API applies its own.
func buildAgentToolsetParams(ctx context.Context, obj types.Object) (*anthropic.BetaManagedAgentsAgentToolset20260401Params, diag.Diagnostics) {
	if !isKnown(obj) {
		return nil, nil
	}
	var toolset agentToolsetModel
	diags := obj.As(ctx, &toolset, basetypes.ObjectAsOptions{})
	if diags.HasError() {
		return nil, diags
	}

	params := &anthropic.BetaManagedAgentsAgentToolset20260401Params{
		Type: anthropic.BetaManagedAgentsAgentToolset20260401ParamsTypeAgentToolset20260401,
	}
	if isKnown(toolset.DefaultEnabled) {
		params.DefaultConfig.Enabled = param.NewOpt(toolset.DefaultEnabled.ValueBool())
	}
	if isKnown(toolset.DefaultPermissionPolicy) {
		params.DefaultConfig.PermissionPolicy = buildAgentToolsetDefaultPermissionPolicy(toolset.DefaultPermissionPolicy.ValueString())
	}

	configs, d := buildAgentToolConfigsParams(ctx, toolset.Configs)
	diags.Append(d...)
	if diags.HasError() {
		return nil, diags
	}
	params.Configs = configs
	return params, diags
}

// buildAgentToolConfigsParams builds the per-tool configs of the agent
// toolset, nil when the list is null or empty (the field is then omitted).
func buildAgentToolConfigsParams(ctx context.Context, list types.List) ([]anthropic.BetaManagedAgentsAgentToolConfigParamsUnion, diag.Diagnostics) {
	if list.IsNull() || len(list.Elements()) == 0 {
		return nil, nil
	}
	var configs []agentToolConfigModel
	diags := list.ElementsAs(ctx, &configs, false)
	if diags.HasError() {
		return nil, diags
	}
	sdkConfigs := make([]anthropic.BetaManagedAgentsAgentToolConfigParamsUnion, len(configs))
	for i, c := range configs {
		cfg, d := buildAgentToolConfigFromModel(ctx, c)
		diags.Append(d...)
		if diags.HasError() {
			return nil, diags
		}
		sdkConfigs[i] = cfg
	}
	return sdkConfigs, diags
}

// buildAgentToolConfigFromModel resolves a planned configs entry and builds
// its union branch. An unset enabled or permission_policy is omitted, so the
// tool inherits the toolset default.
func buildAgentToolConfigFromModel(ctx context.Context, c agentToolConfigModel) (anthropic.BetaManagedAgentsAgentToolConfigParamsUnion, diag.Diagnostics) {
	var enabled param.Opt[bool]
	if isKnown(c.Enabled) {
		enabled = param.NewOpt(c.Enabled.ValueBool())
	}
	var policy string
	if isKnown(c.PermissionPolicy) {
		policy = c.PermissionPolicy.ValueString()
	}
	web, diags := webToolOptionsFromModel(ctx, c)
	if diags.HasError() {
		return anthropic.BetaManagedAgentsAgentToolConfigParamsUnion{}, diags
	}
	cfg, d := buildAgentToolConfigParams(c.Name.ValueString(), enabled, policy, web)
	diags.Append(d...)
	return cfg, diags
}

// buildMCPToolsetsParams builds one mcp_toolset tool per mcp_toolsets entry,
// nil when the list is null or empty.
func buildMCPToolsetsParams(ctx context.Context, list types.List) ([]anthropic.BetaAgentNewParamsToolUnion, diag.Diagnostics) {
	if list.IsNull() || len(list.Elements()) == 0 {
		return nil, nil
	}
	var toolsets []agentMCPToolsetModel
	diags := list.ElementsAs(ctx, &toolsets, false)
	if diags.HasError() {
		return nil, diags
	}
	var tools []anthropic.BetaAgentNewParamsToolUnion
	for _, t := range toolsets {
		p, d := buildMCPToolsetParams(ctx, t)
		diags.Append(d...)
		if diags.HasError() {
			return nil, diags
		}
		tools = append(tools, anthropic.BetaAgentNewParamsToolUnion{OfMCPToolset: p})
	}
	return tools, diags
}

// buildMCPToolsetParams builds an mcp_toolset tool. Defaults and per-tool
// values left unset are omitted.
func buildMCPToolsetParams(ctx context.Context, t agentMCPToolsetModel) (*anthropic.BetaManagedAgentsMCPToolsetParams, diag.Diagnostics) {
	p := &anthropic.BetaManagedAgentsMCPToolsetParams{
		MCPServerName: t.MCPServerName.ValueString(),
		Type:          anthropic.BetaManagedAgentsMCPToolsetParamsTypeMCPToolset,
	}
	if isKnown(t.DefaultEnabled) {
		p.DefaultConfig.Enabled = param.NewOpt(t.DefaultEnabled.ValueBool())
	}
	if isKnown(t.DefaultPermissionPolicy) {
		p.DefaultConfig.PermissionPolicy = buildMCPToolsetDefaultPermissionPolicy(t.DefaultPermissionPolicy.ValueString())
	}
	if t.Configs.IsNull() || len(t.Configs.Elements()) == 0 {
		return p, nil
	}
	var configs []agentMCPToolConfigModel
	diags := t.Configs.ElementsAs(ctx, &configs, false)
	if diags.HasError() {
		return nil, diags
	}
	p.Configs = make([]anthropic.BetaManagedAgentsMCPToolConfigParams, len(configs))
	for i, c := range configs {
		p.Configs[i] = buildMCPToolConfigParams(c)
	}
	return p, diags
}

func buildMCPToolConfigParams(c agentMCPToolConfigModel) anthropic.BetaManagedAgentsMCPToolConfigParams {
	p := anthropic.BetaManagedAgentsMCPToolConfigParams{Name: c.Name.ValueString()}
	if isKnown(c.Enabled) {
		p.Enabled = param.NewOpt(c.Enabled.ValueBool())
	}
	if isKnown(c.PermissionPolicy) {
		p.PermissionPolicy = buildMCPToolConfigPermissionPolicy(c.PermissionPolicy.ValueString())
	}
	return p
}

// buildCustomToolsParams builds one custom tool per custom_tools entry, nil
// when the list is null or empty.
func buildCustomToolsParams(ctx context.Context, list types.List) ([]anthropic.BetaAgentNewParamsToolUnion, diag.Diagnostics) {
	if list.IsNull() || len(list.Elements()) == 0 {
		return nil, nil
	}
	var customTools []agentCustomToolModel
	diags := list.ElementsAs(ctx, &customTools, false)
	if diags.HasError() {
		return nil, diags
	}
	var tools []anthropic.BetaAgentNewParamsToolUnion
	for _, t := range customTools {
		p, d := buildCustomToolParams(t)
		diags.Append(d...)
		if diags.HasError() {
			return nil, diags
		}
		tools = append(tools, anthropic.BetaAgentNewParamsToolUnion{OfCustom: p})
	}
	return tools, diags
}

func buildCustomToolParams(t agentCustomToolModel) (*anthropic.BetaManagedAgentsCustomToolParams, diag.Diagnostics) {
	var diags diag.Diagnostics
	p := &anthropic.BetaManagedAgentsCustomToolParams{
		Name:        t.Name.ValueString(),
		Description: t.Description.ValueString(),
		Type:        anthropic.BetaManagedAgentsCustomToolParamsTypeCustom,
	}
	// The schema reaches the API as configured, root-level keywords such
	// as additionalProperties and $defs included, with "type": "object"
	// added when it has no type. The API requires one, so a tool
	// configured without it is sent as a tool without parameters.
	configured := customToolNoParameters
	if isKnown(t.InputSchema) {
		configured = t.InputSchema.ValueString()
	}
	schema, err := customToolInputSchemaParam(configured)
	if err != nil {
		diags.AddError("Invalid input_schema", fmt.Sprintf("Failed to parse input_schema JSON for tool %q: %s", t.Name.ValueString(), err))
		return nil, diags
	}
	p.InputSchema = schema
	return p, diags
}

// mappingMode tells mapAgentResponseToState which request the response answers.
// Only the tool mapping depends on it.
type mappingMode int

const (
	// readMapping (Read) reports every tool setting the API returns, so a change
	// made outside Terraform shows up as drift.
	readMapping mappingMode = iota
	// applyMapping (Create, Update) keeps the planned tools: `mcp_toolsets` and
	// the `configs` lists are not Computed, so Terraform rejects any other value
	// as an inconsistent result after apply. No MCP toolset or per-tool config
	// the plan lacks is added, and a per-tool field the plan leaves null stays
	// null; a value the API holds beyond the plan shows on the next Read.
	applyMapping
	// importMapping is readMapping for a toolset state does not hold yet (import,
	// or a toolset added out of band). The tool mappers pick it themselves: with
	// no prior configs to compare against, every per-tool config is mapped
	// verbatim, so a configuration that lists one matches state after import.
	importMapping
)

// mapAgentResponseToState maps the API response to the Terraform state model.
// A value the API leaves absent becomes null, a null current value kept as
// is, and a list the API returns empty keeps a configured []
// (listAfterEmptyResponse); metadata, model_speed, the toolsets, the custom
// tools' input_schema, multiagent and execution_identity are correlated with
// the current (planned or prior) value instead.
func mapAgentResponseToState(ctx context.Context, agent *anthropic.BetaManagedAgentsAgent, data *AgentResourceModel, mode mappingMode) diag.Diagnostics {
	data.ID = types.StringValue(agent.ID)
	data.Name = types.StringValue(agent.Name)
	data.Model = types.StringValue(string(agent.Model.ID))
	data.Version = types.Int64Value(agent.Version)
	data.CreatedAt = formatTime(agent.CreatedAt)
	data.UpdatedAt = formatTime(agent.UpdatedAt)
	data.ArchivedAt = tfvalue.TimeOrNull(agent.ArchivedAt)
	data.ModelSpeed = modelSpeedAfterResponse(agent.Model.Speed, data.ModelSpeed)
	data.ModelEffort = modelEffortFromResponse(agent.Model)
	data.ModelInferenceGeo = modelInferenceGeoFromResponse(agent.Model)
	data.Description = stringAfterResponse(agent.Description, data.Description)
	data.System = stringAfterResponse(agent.System, data.System)

	var diags diag.Diagnostics
	if len(agent.Metadata) > 0 {
		metadata, d := metadataValue(agent.Metadata)
		diags.Append(d...)
		data.Metadata = metadata
	} else {
		data.Metadata = tfvalue.MetadataAfterEmptyResponse(data.Metadata)
	}

	servers, d := mcpServersValue(agent.MCPServers)
	diags.Append(d...)
	data.MCPServers = listAfterResponse(ctx, servers, data.MCPServers)

	skills, d := skillsValue(agent.Skills)
	diags.Append(d...)
	data.Skills = listAfterResponse(ctx, skills, data.Skills)

	diags.Append(mapAgentToolsToState(ctx, splitAgentTools(agent.Tools), data, mode)...)

	// Multiagent: correlate with the planned/prior roster (see mapMultiagentToState).
	multiagent, d := mapMultiagentToState(ctx, agent.Multiagent, agent.ID, data.Multiagent)
	diags.Append(d...)
	data.Multiagent = multiagent

	// Execution identity: the echoed default stays null (see mapExecutionIdentityToState).
	identity, d := mapExecutionIdentityToState(agent.ExecutionIdentity, data.ExecutionIdentity)
	diags.Append(d...)
	data.ExecutionIdentity = identity

	return diags
}

// mapAgentToolsToState maps the three kinds of tools onto agent_toolset,
// mcp_toolsets and custom_tools; mode applies to the two toolsets.
func mapAgentToolsToState(ctx context.Context, tools agentTools, data *AgentResourceModel, mode mappingMode) diag.Diagnostics {
	var diags diag.Diagnostics

	if tools.toolset != nil {
		toolset, d := mapAgentToolsetToState(ctx, tools.toolset, data.AgentToolset, mode)
		diags.Append(d...)
		data.AgentToolset = toolset
	} else {
		data.AgentToolset = keepNull(types.ObjectNull(agentToolsetAttrTypes), data.AgentToolset)
	}

	if len(tools.mcp) > 0 {
		mcpToolsets, d := mapMCPToolsetsToState(ctx, tools.mcp, data.MCPToolsets, mode)
		diags.Append(d...)
		data.MCPToolsets = mcpToolsets
	} else {
		data.MCPToolsets = listAfterEmptyResponse(data.MCPToolsets, types.ObjectType{AttrTypes: agentMCPToolsetAttrTypes})
	}

	customTools, d := customToolsValue(tools.custom, customToolInputSchemaState(customToolInputSchemas(ctx, data.CustomTools)))
	diags.Append(d...)
	data.CustomTools = listAfterResponse(ctx, customTools, data.CustomTools)
	return diags
}

// listAfterEmptyResponse is the state value of a list attribute the API
// returns empty. The API returns [] both for an attribute never set and for
// one set to [], so a known empty list stays as configured and a null one
// stays null; anything else becomes null. Mapping a configured [] to null
// failed the apply with "Provider produced inconsistent result after apply"
// (tfvalue.MetadataAfterEmptyResponse does the same for metadata).
func listAfterEmptyResponse(current types.List, elemType attr.Type) types.List {
	if current.IsNull() || (!current.IsUnknown() && len(current.Elements()) == 0) {
		return current
	}
	return types.ListNull(elemType)
}

// listAfterResponse is the state value of a list attribute mapped from the
// response: v when the API returned elements, else listAfterEmptyResponse.
func listAfterResponse(ctx context.Context, v, current types.List) types.List {
	if v.IsNull() {
		return listAfterEmptyResponse(current, v.ElementType(ctx))
	}
	return v
}

// apiToolConfig is the response-side shape shared by built-in and MCP per-tool
// configs, so a single mapping serves both toolset kinds. extra holds the
// attributes only one kind has, already mapped verbatim (the web options of a
// built-in tool, see webToolOptionsFromResponse); it is nil for MCP tools.
type apiToolConfig struct {
	name             string
	enabled          bool
	permissionPolicy string
	extra            map[string]attr.Value
}

// values returns the attribute values of the config object, with `enabled`
// and `permission_policy` as resolved by the caller.
func (c apiToolConfig) values(enabled types.Bool, permissionPolicy types.String) map[string]attr.Value {
	values := map[string]attr.Value{
		"name":              types.StringValue(c.name),
		"enabled":           enabled,
		"permission_policy": permissionPolicy,
	}
	maps.Copy(values, c.extra)
	return values
}

// trackedToolConfig is what mapToolConfigsToState needs from a configured
// entry. It is read attribute by attribute because built-in and MCP entries
// have different object types; attrs keeps them all for the extra attributes
// of a kind (webToolOptionsToState).
type trackedToolConfig struct {
	name         string
	enabledUnset bool
	policyUnset  bool
	attrs        map[string]attr.Value
}

func trackedToolConfigs(current types.List) []trackedToolConfig {
	var tracked []trackedToolConfig
	for _, el := range current.Elements() {
		obj, ok := el.(types.Object)
		if !ok || obj.IsNull() || obj.IsUnknown() {
			continue
		}
		attrs := obj.Attributes()
		name, _ := attrs["name"].(types.String)
		enabled, _ := attrs["enabled"].(types.Bool)
		policy, _ := attrs["permission_policy"].(types.String)
		tracked = append(tracked, trackedToolConfig{
			name:         name.ValueString(),
			enabledUnset: enabled.IsNull(),
			policyUnset:  policy.IsNull(),
			attrs:        attrs,
		})
	}
	return tracked
}

// mapToolConfigsToState maps a toolset's per-tool configs from the API into
// state, against current: the prior state on Read, the plan on Create and
// Update. On Read every config that changes something is represented, so an
// override added out of band (a config for another tool, or a policy relaxed to
// `always_allow`) shows up as a plan diff instead of being dropped:
//   - configs tracked in current come first, in their order; a tracked config
//     the API no longer returns is omitted, so it is re-created on apply;
//   - configs the API returns for any other tool are appended in API order,
//     except one whose `enabled` and `permission_policy` both equal the toolset
//     defaults: it changes nothing, and if the API lists such configs for tools
//     never configured, reporting them would leave a diff that never clears;
//   - a field the configuration left unset stays null only while the API value
//     equals the toolset default it inherits (the docs: per-tool configs
//     override `default_config`), so an echoed default causes no perpetual diff
//     and a diverging value does;
//   - the extra attributes of a kind (the web options of built-in tools) have
//     no inherited default; a tracked config resolves them against current
//     with webToolOptionsToState.
//
// A null current (a toolset state holds without a configs block) maps every
// other API config verbatim, and stays null when there is none.
//
// With importMapping (a toolset state does not hold yet: import, or one added
// out of band) current is null and every API config is mapped verbatim, the
// ones equal to the defaults included: there is no configuration to tell them
// apart from, and a configuration that lists one must match state after import.
//
// With applyMapping (Create, Update) a null current stays null, configs for
// other tools are not appended and a field the plan leaves null stays null:
// what differs from the plan shows on the next Read.
func mapToolConfigsToState(current types.List, apiConfigs []apiToolConfig, defaultEnabled bool, defaultPolicy string, attrTypes map[string]attr.Type, mode mappingMode) (types.List, diag.Diagnostics) {
	var diags diag.Diagnostics
	elemType := types.ObjectType{AttrTypes: attrTypes}

	var stateConfigs []trackedToolConfig
	switch {
	case !current.IsNull() && !current.IsUnknown():
		stateConfigs = trackedToolConfigs(current)
	case len(apiConfigs) == 0, current.IsNull() && mode == applyMapping:
		return types.ListNull(elemType), diags
	}

	apiByName := make(map[string]apiToolConfig, len(apiConfigs))
	for _, c := range apiConfigs {
		apiByName[c.name] = c
	}

	configObjs := make([]attr.Value, 0, len(apiConfigs))
	tracked := make(map[string]bool, len(stateConfigs))
	for _, sc := range stateConfigs {
		tracked[sc.name] = true
		apiCfg, ok := apiByName[sc.name]
		if !ok {
			continue
		}
		obj, d := trackedToolConfigValue(sc, apiCfg, defaultEnabled, defaultPolicy, attrTypes, mode)
		diags.Append(d...)
		configObjs = append(configObjs, obj)
	}

	for _, c := range apiConfigs {
		if tracked[c.name] || !appendsUntrackedToolConfig(c, defaultEnabled, defaultPolicy, mode) {
			continue
		}
		tracked[c.name] = true
		obj, d := types.ObjectValue(attrTypes, c.values(types.BoolValue(c.enabled), types.StringValue(c.permissionPolicy)))
		diags.Append(d...)
		configObjs = append(configObjs, obj)
	}
	if len(configObjs) == 0 && (current.IsNull() || current.IsUnknown()) {
		return types.ListNull(elemType), diags
	}

	list, d := types.ListValue(elemType, configObjs)
	diags.Append(d...)
	return list, diags
}

// trackedToolConfigValue maps the API config of a tool current tracks. A field
// the configuration left unset stays null while the API value equals the
// toolset default it inherits, and always with applyMapping; the web options
// of a built-in tool resolve against current (webToolOptionsToState).
func trackedToolConfigValue(sc trackedToolConfig, apiCfg apiToolConfig, defaultEnabled bool, defaultPolicy string, attrTypes map[string]attr.Type, mode mappingMode) (types.Object, diag.Diagnostics) {
	enabled := types.BoolValue(apiCfg.enabled)
	if sc.enabledUnset && (mode == applyMapping || apiCfg.enabled == defaultEnabled) {
		enabled = types.BoolNull()
	}
	permPolicy := types.StringValue(apiCfg.permissionPolicy)
	if sc.policyUnset && (mode == applyMapping || apiCfg.permissionPolicy == defaultPolicy) {
		permPolicy = types.StringNull()
	}
	if apiCfg.extra != nil {
		apiCfg.extra = webToolOptionsToState(sc.attrs, apiCfg.extra, mode)
	}
	return types.ObjectValue(attrTypes, apiCfg.values(enabled, permPolicy))
}

// appendsUntrackedToolConfig reports whether the API config of a tool current
// does not track goes into state: never with applyMapping, on Read only when
// it differs from the toolset defaults, and always with importMapping.
func appendsUntrackedToolConfig(c apiToolConfig, defaultEnabled bool, defaultPolicy string, mode mappingMode) bool {
	switch mode {
	case applyMapping:
		return false
	case readMapping:
		return c.enabled != defaultEnabled || c.permissionPolicy != defaultPolicy
	default:
		return true
	}
}

// mapAgentToolsetToState maps the API agent toolset response to a Terraform object.
// Per-tool configs go through mapToolConfigsToState, so out-of-band overrides show up on Read;
// with no toolset in state (import), they are mapped with importMapping.
func mapAgentToolsetToState(ctx context.Context, apiToolset *anthropic.BetaManagedAgentsAgentToolset20260401, currentState types.Object, mode mappingMode) (types.Object, diag.Diagnostics) {
	var diags diag.Diagnostics

	defaultEnabled := types.BoolValue(apiToolset.DefaultConfig.Enabled)
	defaultPermPolicy := types.StringValue(apiToolset.DefaultConfig.PermissionPolicy.Type)

	currentConfigs := types.ListNull(types.ObjectType{AttrTypes: agentToolConfigAttrTypes})
	if !currentState.IsNull() && !currentState.IsUnknown() {
		var stateToolset agentToolsetModel
		diags.Append(currentState.As(ctx, &stateToolset, basetypes.ObjectAsOptions{})...)
		if diags.HasError() {
			return types.ObjectNull(agentToolsetAttrTypes), diags
		}
		currentConfigs = stateToolset.Configs
	} else if mode == readMapping {
		mode = importMapping
	}

	apiConfigs := make([]apiToolConfig, 0, len(apiToolset.Configs))
	for _, c := range apiToolset.Configs {
		cfg, d := apiAgentToolConfig(c)
		diags.Append(d...)
		apiConfigs = append(apiConfigs, cfg)
	}
	if diags.HasError() {
		return types.ObjectNull(agentToolsetAttrTypes), diags
	}

	configsList, d := mapToolConfigsToState(currentConfigs, apiConfigs,
		apiToolset.DefaultConfig.Enabled, apiToolset.DefaultConfig.PermissionPolicy.Type, agentToolConfigAttrTypes, mode)
	diags.Append(d...)
	if diags.HasError() {
		return types.ObjectNull(agentToolsetAttrTypes), diags
	}

	obj, d := types.ObjectValue(agentToolsetAttrTypes, map[string]attr.Value{
		"default_enabled":           defaultEnabled,
		"default_permission_policy": defaultPermPolicy,
		"configs":                   configsList,
	})
	diags.Append(d...)
	return obj, diags
}

// mapMCPToolsetsToState maps API MCP toolsets to a Terraform list, matching by server name.
// Toolsets tracked in current state come first, in state order; on Read a toolset the API
// returns for any other server is appended in API order, so a toolset added out of band
// surfaces as a plan diff and is removed on apply; its per-tool configs (as on import) are
// mapped with importMapping. With applyMapping (Create, Update) only planned toolsets are
// mapped, and a null plan stays null. Per-tool configs go through mapToolConfigsToState.
func mapMCPToolsetsToState(ctx context.Context, apiToolsets []anthropic.BetaManagedAgentsMCPToolset, currentState types.List, mode mappingMode) (types.List, diag.Diagnostics) {
	var diags diag.Diagnostics
	if mode == applyMapping && currentState.IsNull() {
		return types.ListNull(types.ObjectType{AttrTypes: agentMCPToolsetAttrTypes}), diags
	}

	apiByServer := map[string]anthropic.BetaManagedAgentsMCPToolset{}
	for _, t := range apiToolsets {
		apiByServer[t.MCPServerName] = t
	}

	var stateToolsets []agentMCPToolsetModel
	if !currentState.IsNull() && !currentState.IsUnknown() {
		diags.Append(currentState.ElementsAs(ctx, &stateToolsets, false)...)
		if diags.HasError() {
			return types.ListNull(types.ObjectType{AttrTypes: agentMCPToolsetAttrTypes}), diags
		}
	}

	toolsetObjs := make([]attr.Value, 0, len(apiToolsets))
	mapped := make(map[string]bool, len(apiToolsets))
	appendToolset := func(apiToolset anthropic.BetaManagedAgentsMCPToolset, currentConfigs types.List, configsMode mappingMode) {
		mapped[apiToolset.MCPServerName] = true

		apiConfigs := make([]apiToolConfig, 0, len(apiToolset.Configs))
		for _, c := range apiToolset.Configs {
			apiConfigs = append(apiConfigs, apiToolConfig{
				name:             c.Name,
				enabled:          c.Enabled,
				permissionPolicy: c.PermissionPolicy.Type,
			})
		}
		configsList, d := mapToolConfigsToState(currentConfigs, apiConfigs,
			apiToolset.DefaultConfig.Enabled, apiToolset.DefaultConfig.PermissionPolicy.Type, agentMCPToolConfigAttrTypes, configsMode)
		diags.Append(d...)

		obj, d := types.ObjectValue(agentMCPToolsetAttrTypes, map[string]attr.Value{
			"mcp_server_name":           types.StringValue(apiToolset.MCPServerName),
			"default_enabled":           types.BoolValue(apiToolset.DefaultConfig.Enabled),
			"default_permission_policy": types.StringValue(apiToolset.DefaultConfig.PermissionPolicy.Type),
			"configs":                   configsList,
		})
		diags.Append(d...)
		toolsetObjs = append(toolsetObjs, obj)
	}

	for _, st := range stateToolsets {
		serverName := st.MCPServerName.ValueString()
		apiToolset, exists := apiByServer[serverName]
		if !exists || mapped[serverName] {
			continue
		}
		appendToolset(apiToolset, st.Configs, mode)
	}
	for _, apiToolset := range apiToolsets {
		if mode == applyMapping || mapped[apiToolset.MCPServerName] {
			continue
		}
		appendToolset(apiToolset, types.ListNull(types.ObjectType{AttrTypes: agentMCPToolConfigAttrTypes}), importMapping)
	}
	if diags.HasError() {
		return types.ListNull(types.ObjectType{AttrTypes: agentMCPToolsetAttrTypes}), diags
	}

	list, d := types.ListValue(types.ObjectType{AttrTypes: agentMCPToolsetAttrTypes}, toolsetObjs)
	diags.Append(d...)
	return list, diags
}

// --- Permission policy builder helpers ---

// alwaysAllowPolicyParam, alwaysAskPolicyParam and autoPolicyParam build the
// three permission policy variants
// (https://platform.claude.com/docs/en/managed-agents/permission-policies).
func alwaysAllowPolicyParam() *anthropic.BetaManagedAgentsAlwaysAllowPolicyParam {
	return &anthropic.BetaManagedAgentsAlwaysAllowPolicyParam{
		Type: anthropic.BetaManagedAgentsAlwaysAllowPolicyTypeAlwaysAllow,
	}
}

func alwaysAskPolicyParam() *anthropic.BetaManagedAgentsAlwaysAskPolicyParam {
	return &anthropic.BetaManagedAgentsAlwaysAskPolicyParam{
		Type: anthropic.BetaManagedAgentsAlwaysAskPolicyTypeAlwaysAsk,
	}
}

func autoPolicyParam() *anthropic.BetaManagedAgentsAutoPolicyParam {
	p := anthropic.NewBetaManagedAgentsAutoPolicyParam()
	return &p
}

// permissionPolicyParams returns the variants of a permission policy union to
// fill for policyType: one non-nil pointer for a known policy, none for ""
// (the union is then omitted and the toolset default applies). Since SDK
// v1.66.0 every built-in tool, the MCP tool configs and both toolset defaults
// carry their own `PermissionPolicyUnion` type, but all of them are unions over
// these same three params, so callers assign the result to their union's
// OfAlwaysAllow, OfAlwaysAsk and OfAuto fields. permissionPolicyValidator
// rejects any other value at plan time.
func permissionPolicyParams(policyType string) (*anthropic.BetaManagedAgentsAlwaysAllowPolicyParam, *anthropic.BetaManagedAgentsAlwaysAskPolicyParam, *anthropic.BetaManagedAgentsAutoPolicyParam) {
	switch policyType {
	case policyAlwaysAllow:
		return alwaysAllowPolicyParam(), nil, nil
	case policyAlwaysAsk:
		return nil, alwaysAskPolicyParam(), nil
	case policyAuto:
		return nil, nil, autoPolicyParam()
	}
	return nil, nil, nil
}

func buildAgentToolsetDefaultPermissionPolicy(policyType string) anthropic.BetaManagedAgentsAgentToolsetDefaultConfigParamsPermissionPolicyUnion {
	var u anthropic.BetaManagedAgentsAgentToolsetDefaultConfigParamsPermissionPolicyUnion
	u.OfAlwaysAllow, u.OfAlwaysAsk, u.OfAuto = permissionPolicyParams(policyType)
	return u
}

func buildMCPToolConfigPermissionPolicy(policyType string) anthropic.BetaManagedAgentsMCPToolConfigParamsPermissionPolicyUnion {
	var u anthropic.BetaManagedAgentsMCPToolConfigParamsPermissionPolicyUnion
	u.OfAlwaysAllow, u.OfAlwaysAsk, u.OfAuto = permissionPolicyParams(policyType)
	return u
}

func buildMCPToolsetDefaultPermissionPolicy(policyType string) anthropic.BetaManagedAgentsMCPToolsetDefaultConfigParamsPermissionPolicyUnion {
	var u anthropic.BetaManagedAgentsMCPToolsetDefaultConfigParamsPermissionPolicyUnion
	u.OfAlwaysAllow, u.OfAlwaysAsk, u.OfAuto = permissionPolicyParams(policyType)
	return u
}
