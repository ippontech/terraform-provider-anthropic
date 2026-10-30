// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	dsschema "github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

// Roster entry types.
const (
	multiagentEntryAgent   = "agent"
	multiagentEntrySelf    = "self"
	multiagentEntryAdvisor = "advisor"
)

// Multiagent types: the coordinator roster, and the multiagent_20261001
// configuration (SDK v1.80.0) whose subagents, workflows and advisor are each
// enabled or disabled on their own
// (https://platform.claude.com/docs/en/managed-agents/multiagent-orchestration).
const (
	multiagentTypeCoordinator = "coordinator"
	multiagentType20261001    = "multiagent_20261001"
	multiagentMemberEnabled   = "enabled"
	multiagentMemberDisabled  = "disabled"
)

type agentMultiagentModel struct {
	Type      types.String `tfsdk:"type"`
	Agents    types.List   `tfsdk:"agents"`
	Subagents types.Object `tfsdk:"subagents"`
	Workflows types.Object `tfsdk:"workflows"`
	Advisor   types.Object `tfsdk:"advisor"`
}

type agentMultiagentEntryModel struct {
	Type    types.String `tfsdk:"type"`
	ID      types.String `tfsdk:"id"`
	Version types.Int64  `tfsdk:"version"`
	Model   types.String `tfsdk:"model"`
}

// agentMultiagentMemberModel is the `subagents` or `workflows` setting of a
// multiagent_20261001 configuration. Unset fields take the API defaults:
// enabled, with inline agents, and no predefined agents.
type agentMultiagentMemberModel struct {
	Enabled             types.Bool `tfsdk:"enabled"`
	InlineAgentsEnabled types.Bool `tfsdk:"inline_agents_enabled"`
	PredefinedAgents    types.List `tfsdk:"predefined_agents"`
}

// agentMultiagentPredefinedAgentModel is a `predefined_agents` entry: the
// coordinator roster entry shape without the advisor's `model`.
type agentMultiagentPredefinedAgentModel struct {
	Type    types.String `tfsdk:"type"`
	ID      types.String `tfsdk:"id"`
	Version types.Int64  `tfsdk:"version"`
}

type agentMultiagentAdvisorModel struct {
	Model types.String `tfsdk:"model"`
}

var agentMultiagentEntryAttrTypes = map[string]attr.Type{
	"type":    types.StringType,
	"id":      types.StringType,
	"version": types.Int64Type,
	"model":   types.StringType,
}

var agentMultiagentPredefinedAgentAttrTypes = map[string]attr.Type{
	"type":    types.StringType,
	"id":      types.StringType,
	"version": types.Int64Type,
}

var agentMultiagentMemberAttrTypes = map[string]attr.Type{
	"enabled":               types.BoolType,
	"inline_agents_enabled": types.BoolType,
	"predefined_agents":     types.ListType{ElemType: types.ObjectType{AttrTypes: agentMultiagentPredefinedAgentAttrTypes}},
}

var agentMultiagentAdvisorAttrTypes = map[string]attr.Type{
	"model": types.StringType,
}

var agentMultiagentAttrTypes = map[string]attr.Type{
	"type":      types.StringType,
	"agents":    types.ListType{ElemType: types.ObjectType{AttrTypes: agentMultiagentEntryAttrTypes}},
	"subagents": types.ObjectType{AttrTypes: agentMultiagentMemberAttrTypes},
	"workflows": types.ObjectType{AttrTypes: agentMultiagentMemberAttrTypes},
	"advisor":   types.ObjectType{AttrTypes: agentMultiagentAdvisorAttrTypes},
}

// --- Schema ---

// multiagentMemberSchema is the `subagents` or `workflows` attribute of the
// resource's multiagent object.
func multiagentMemberSchema(name, description string) schema.SingleNestedAttribute {
	return schema.SingleNestedAttribute{
		Optional:            true,
		MarkdownDescription: description,
		Attributes: map[string]schema.Attribute{
			"enabled": schema.BoolAttribute{
				Optional:            true,
				MarkdownDescription: "Whether the setting is on. Defaults to `true`. When `false`, the other two attributes must be unset.",
			},
			"inline_agents_enabled": schema.BoolAttribute{
				Optional: true,
				MarkdownDescription: "Whether inline agents, which are defined on the fly and never saved, are allowed besides `predefined_agents`. " +
					"Defaults to `true`. When `false`, `predefined_agents` must list at least one agent: " +
					"set it to `false` under both `subagents` and `workflows` to allow only the agents you list.",
			},
			"predefined_agents": schema.ListNestedAttribute{
				Optional: true,
				MarkdownDescription: "Saved agents the setting can use, 1-20 entries referencing distinct agents and at most one `self`. " +
					"Separate from the other setting's list: an agent in one list is not added to the other. Omitted, the list is empty.",
				Validators: []validator.List{listvalidator.SizeBetween(1, 20)},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"type": schema.StringAttribute{
							Required:            true,
							MarkdownDescription: "Entry type: `agent` (another agent) or `self` (copies of this agent).",
							Validators:          []validator.String{stringvalidator.OneOf(multiagentEntryAgent, multiagentEntrySelf)},
						},
						"id": schema.StringAttribute{
							Optional:            true,
							MarkdownDescription: "ID of the referenced agent. Required when `type` is `agent`, forbidden otherwise.",
						},
						"version": schema.Int64Attribute{
							Optional: true,
							Computed: true,
							MarkdownDescription: "Pinned version of the referenced agent (`agent` entries only). " +
								"The API pins the latest version when omitted and echoes it back.",
						},
					},
				},
			},
		},
	}
}

// multiagentDataSourceSchema is the read-only `multiagent` attribute of the
// agent and agents data sources.
func multiagentDataSourceSchema() dsschema.SingleNestedAttribute {
	member := func(description string) dsschema.SingleNestedAttribute {
		return dsschema.SingleNestedAttribute{
			Computed:            true,
			MarkdownDescription: description,
			Attributes: map[string]dsschema.Attribute{
				"enabled":               dsschema.BoolAttribute{Computed: true, MarkdownDescription: "Whether the setting is on."},
				"inline_agents_enabled": dsschema.BoolAttribute{Computed: true, MarkdownDescription: "Whether inline agents are allowed, null when the setting is off."},
				"predefined_agents": dsschema.ListNestedAttribute{
					Computed:            true,
					MarkdownDescription: "Resolved predefined agents, null when there are none.",
					NestedObject: dsschema.NestedAttributeObject{
						Attributes: map[string]dsschema.Attribute{
							"type":    dsschema.StringAttribute{Computed: true, MarkdownDescription: "Entry type (`agent`)."},
							"id":      dsschema.StringAttribute{Computed: true, MarkdownDescription: "Referenced agent ID."},
							"version": dsschema.Int64Attribute{Computed: true, MarkdownDescription: "Resolved version of the referenced agent."},
						},
					},
				},
			},
		}
	}
	return dsschema.SingleNestedAttribute{
		Computed: true,
		MarkdownDescription: "Multiagent orchestration, null when the agent has none. A `self` entry is resolved by the API into an `agent` entry " +
			"carrying the agent's own ID.",
		Attributes: map[string]dsschema.Attribute{
			"type": dsschema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "`coordinator` or `multiagent_20261001`.",
			},
			"agents": dsschema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Resolved roster of agents the coordinator may spawn (`coordinator` only).",
				NestedObject: dsschema.NestedAttributeObject{
					Attributes: map[string]dsschema.Attribute{
						"type":    dsschema.StringAttribute{Computed: true, MarkdownDescription: "Entry type: `agent` or `advisor`."},
						"id":      dsschema.StringAttribute{Computed: true, MarkdownDescription: "Referenced agent ID (`agent` entries)."},
						"version": dsschema.Int64Attribute{Computed: true, MarkdownDescription: "Resolved version of the referenced agent (`agent` entries)."},
						"model":   dsschema.StringAttribute{Computed: true, MarkdownDescription: "Advisor model ID (`advisor` entries)."},
					},
				},
			},
			"subagents": member("Delegation to subagents (`multiagent_20261001` only)."),
			"workflows": member("Dynamic workflows (`multiagent_20261001` only)."),
			"advisor": dsschema.SingleNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Advisor model of a `multiagent_20261001` configuration, null when disabled.",
				Attributes: map[string]dsschema.Attribute{
					"model": dsschema.StringAttribute{Computed: true, MarkdownDescription: "Advisor model ID."},
				},
			},
		},
	}
}

// --- Config validation ---

// multiagentConfigValidator enforces the cross-attribute rules of the roster.
type multiagentConfigValidator struct{}

func (v *multiagentConfigValidator) Description(_ context.Context) string {
	return "Validates the multiagent configuration: coordinator roster entries (id for agent entries, model for advisor entries, " +
		"at most one self and one advisor), and the type-specific attributes and predefined agents of multiagent_20261001."
}

func (v *multiagentConfigValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v *multiagentConfigValidator) ValidateResource(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var obj types.Object
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("multiagent"), &obj)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateMultiagentConfig(ctx, obj)...)
}

// validateMultiagentConfig checks the multiagent rules. Unknown values count as
// neither set nor missing, so configs referencing not-yet-known values stay
// valid at plan time.
func validateMultiagentConfig(ctx context.Context, obj types.Object) diag.Diagnostics {
	var diags diag.Diagnostics
	if obj.IsNull() || obj.IsUnknown() {
		return diags
	}
	var ma agentMultiagentModel
	diags.Append(obj.As(ctx, &ma, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return diags
	}

	isSet := func(v attr.Value) bool { return !v.IsNull() && !v.IsUnknown() }
	root := path.Root("multiagent")
	switch ma.Type.ValueString() {
	case multiagentTypeCoordinator:
		if ma.Agents.IsNull() {
			diags.AddAttributeError(root.AtName("agents"), "Missing roster", `"agents" is required when type is "coordinator".`)
		}
		for _, f := range []struct {
			name  string
			value types.Object
		}{
			{"subagents", ma.Subagents},
			{"workflows", ma.Workflows},
			{"advisor", ma.Advisor},
		} {
			if isSet(f.value) {
				diags.AddAttributeError(root.AtName(f.name), "Unexpected multiagent setting",
					fmt.Sprintf(`%q is only allowed when type is %q.`, f.name, multiagentType20261001))
			}
		}
	case multiagentType20261001:
		if isSet(ma.Agents) {
			diags.AddAttributeError(root.AtName("agents"), "Unexpected roster",
				fmt.Sprintf(`"agents" is only allowed when type is %q: list the agents in "subagents" or "workflows" instead.`, multiagentTypeCoordinator))
		}
	}
	diags.Append(validateMultiagentMember(ctx, ma.Subagents, root.AtName("subagents"))...)
	diags.Append(validateMultiagentMember(ctx, ma.Workflows, root.AtName("workflows"))...)
	if diags.HasError() || ma.Agents.IsNull() || ma.Agents.IsUnknown() {
		return diags
	}
	diags.Append(validateMultiagentRoster(ctx, ma.Agents)...)
	return diags
}

// validateMultiagentRoster checks the coordinator roster entries: each entry
// in list order (validateRosterEntry), then at most one self and one advisor.
func validateMultiagentRoster(ctx context.Context, agents types.List) diag.Diagnostics {
	entries, diags := decodeRosterEntries(ctx, agents)
	if diags.HasError() {
		return diags
	}

	roster := path.Root("multiagent").AtName("agents")
	counts := map[string]int{}
	seenIDs := map[string]bool{}
	for i, e := range entries {
		if !isKnown(e.Type) {
			continue
		}
		counts[e.Type.ValueString()]++
		diags.Append(validateRosterEntry(e, roster.AtListIndex(i), seenIDs)...)
	}
	if counts[multiagentEntrySelf] > 1 {
		diags.AddAttributeError(roster, "Duplicate self entry", `At most one roster entry may have type "self".`)
	}
	if counts[multiagentEntryAdvisor] > 1 {
		diags.AddAttributeError(roster, "Duplicate advisor entry", `At most one roster entry may have type "advisor".`)
	}
	return diags
}

// decodeRosterEntries decodes the roster element by element: ElementsAs fails
// on a single unknown element object (e.g. a conditional entry), which must
// stay valid at plan time. Unknown elements keep a zero-valued slot (null
// type), which the checks skip.
func decodeRosterEntries(ctx context.Context, agents types.List) ([]agentMultiagentEntryModel, diag.Diagnostics) {
	var diags diag.Diagnostics
	elems := agents.Elements()
	entries := make([]agentMultiagentEntryModel, len(elems))
	for i, el := range elems {
		o, ok := el.(types.Object)
		if !ok || !isKnown(o) {
			continue
		}
		diags.Append(o.As(ctx, &entries[i], basetypes.ObjectAsOptions{})...)
	}
	return entries, diags
}

// validateRosterEntry checks one roster entry of known type at p: the
// attributes its type requires and forbids, and, for an agent, an ID no
// earlier entry references (seenIDs).
func validateRosterEntry(e agentMultiagentEntryModel, p path.Path, seenIDs map[string]bool) diag.Diagnostics {
	var diags diag.Diagnostics
	switch e.Type.ValueString() {
	case multiagentEntryAgent:
		if isKnown(e.ID) {
			if seenIDs[e.ID.ValueString()] {
				diags.AddAttributeError(path.Root("multiagent").AtName("agents"), "Duplicate agent entry",
					fmt.Sprintf("Roster entries must reference distinct agents; %q appears more than once.", e.ID.ValueString()))
			}
			seenIDs[e.ID.ValueString()] = true
		}
		if e.ID.IsNull() {
			diags.AddAttributeError(p.AtName("id"), "Missing roster agent ID", `"id" is required when type is "agent".`)
		}
		diags.Append(rejectRosterModel(e, p)...)
	case multiagentEntrySelf:
		diags.Append(rejectRosterID(e, p)...)
		diags.Append(rejectRosterModel(e, p)...)
		diags.Append(rejectRosterVersion(e, p)...)
	case multiagentEntryAdvisor:
		if e.Model.IsNull() {
			diags.AddAttributeError(p.AtName("model"), "Missing advisor model", `"model" is required when type is "advisor".`)
		}
		diags.Append(rejectRosterID(e, p)...)
		diags.Append(rejectRosterVersion(e, p)...)
	}
	return diags
}

func rejectRosterID(e agentMultiagentEntryModel, p path.Path) diag.Diagnostics {
	var diags diag.Diagnostics
	if isKnown(e.ID) {
		diags.AddAttributeError(p.AtName("id"), "Unexpected roster agent ID", `"id" is only allowed when type is "agent".`)
	}
	return diags
}

func rejectRosterModel(e agentMultiagentEntryModel, p path.Path) diag.Diagnostics {
	var diags diag.Diagnostics
	if isKnown(e.Model) {
		diags.AddAttributeError(p.AtName("model"), "Unexpected roster model", `"model" is only allowed when type is "advisor".`)
	}
	return diags
}

func rejectRosterVersion(e agentMultiagentEntryModel, p path.Path) diag.Diagnostics {
	var diags diag.Diagnostics
	if isKnown(e.Version) {
		diags.AddAttributeError(p.AtName("version"), "Unexpected roster version", `"version" is only allowed when type is "agent".`)
	}
	return diags
}

// validateMultiagentMember checks a `subagents` or `workflows` setting: nothing
// else set when it is disabled, at least one predefined agent when inline
// agents are off (the API rejects it otherwise), and the predefined entries.
func validateMultiagentMember(ctx context.Context, obj types.Object, p path.Path) diag.Diagnostics {
	var diags diag.Diagnostics
	if !isKnown(obj) {
		return diags
	}
	var m agentMultiagentMemberModel
	diags.Append(obj.As(ctx, &m, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return diags
	}

	if isKnown(m.Enabled) && !m.Enabled.ValueBool() {
		diags.Append(validateDisabledMember(m, p)...)
		return diags
	}
	if isKnown(m.InlineAgentsEnabled) && !m.InlineAgentsEnabled.ValueBool() && m.PredefinedAgents.IsNull() {
		diags.AddAttributeError(p.AtName("predefined_agents"), "Missing predefined agents",
			`"predefined_agents" must list at least one agent when "inline_agents_enabled" is false.`)
	}
	if !isKnown(m.PredefinedAgents) {
		return diags
	}
	diags.Append(validatePredefinedAgents(ctx, m.PredefinedAgents, p.AtName("predefined_agents"))...)
	return diags
}

// validateDisabledMember rejects the other attributes of a disabled setting.
func validateDisabledMember(m agentMultiagentMemberModel, p path.Path) diag.Diagnostics {
	var diags diag.Diagnostics
	for _, f := range []struct {
		name  string
		value attr.Value
	}{
		{"inline_agents_enabled", m.InlineAgentsEnabled},
		{"predefined_agents", m.PredefinedAgents},
	} {
		if isKnown(f.value) {
			diags.AddAttributeError(p.AtName(f.name), "Unexpected setting on a disabled member",
				fmt.Sprintf(`%q is only allowed when "enabled" is true.`, f.name))
		}
	}
	return diags
}

// validatePredefinedAgents checks the known entries of a predefined_agents
// list at p: each entry (validatePredefinedAgent), then at most one self. An
// entry that cannot be decoded stops the checks.
func validatePredefinedAgents(ctx context.Context, list types.List, p path.Path) diag.Diagnostics {
	var diags diag.Diagnostics
	selfs := 0
	seenIDs := map[string]bool{}
	for i, el := range list.Elements() {
		o, ok := el.(types.Object)
		if !ok || !isKnown(o) {
			continue
		}
		var e agentMultiagentPredefinedAgentModel
		if d := o.As(ctx, &e, basetypes.ObjectAsOptions{}); d.HasError() {
			diags.Append(d...)
			return diags
		}
		if e.Type.ValueString() == multiagentEntrySelf {
			selfs++
		}
		diags.Append(validatePredefinedAgent(e, p, i, seenIDs)...)
	}
	if selfs > 1 {
		diags.AddAttributeError(p, "Duplicate self entry", `At most one predefined agent may have type "self".`)
	}
	return diags
}

// validatePredefinedAgent checks entry i of the predefined_agents list at p:
// an agent needs an ID no earlier entry references (seenIDs), a self takes
// neither ID nor version.
func validatePredefinedAgent(e agentMultiagentPredefinedAgentModel, p path.Path, i int, seenIDs map[string]bool) diag.Diagnostics {
	var diags diag.Diagnostics
	ep := p.AtListIndex(i)
	switch e.Type.ValueString() {
	case multiagentEntryAgent:
		switch {
		case e.ID.IsNull():
			diags.AddAttributeError(ep.AtName("id"), "Missing predefined agent ID", `"id" is required when type is "agent".`)
		case e.ID.IsUnknown():
			// Not known yet: neither missing nor a duplicate.
		case seenIDs[e.ID.ValueString()]:
			diags.AddAttributeError(p, "Duplicate predefined agent",
				fmt.Sprintf("Predefined agents must be distinct; %q appears more than once.", e.ID.ValueString()))
		default:
			seenIDs[e.ID.ValueString()] = true
		}
	case multiagentEntrySelf:
		if isKnown(e.ID) {
			diags.AddAttributeError(ep.AtName("id"), "Unexpected predefined agent ID", `"id" is only allowed when type is "agent".`)
		}
		if isKnown(e.Version) {
			diags.AddAttributeError(ep.AtName("version"), "Unexpected predefined agent version", `"version" is only allowed when type is "agent".`)
		}
	}
	return diags
}

// --- Request building ---

// buildMultiagentParams converts the planned multiagent object into SDK params,
// on the union branch its `type` selects. It returns the zero value (omitted by
// the SDK) for a null or unknown object.
//
// prior is the multiagent object from state (null on create). An `agent` entry
// whose planned version is unknown (not set in config) reuses the version prior
// state holds for the same id in the same list, so an unrelated update does not
// re-pin the member to its latest version; a member absent from prior state is
// sent without version (API pins latest).
func buildMultiagentParams(ctx context.Context, obj, prior types.Object) (anthropic.BetaManagedAgentsMultiagentParamsUnion, diag.Diagnostics) {
	var diags diag.Diagnostics
	var out anthropic.BetaManagedAgentsMultiagentParamsUnion
	if obj.IsNull() || obj.IsUnknown() {
		return out, diags
	}
	var ma agentMultiagentModel
	diags.Append(obj.As(ctx, &ma, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return out, diags
	}
	var pm agentMultiagentModel
	priorDecoded := !prior.IsNull() && !prior.IsUnknown() && !prior.As(ctx, &pm, basetypes.ObjectAsOptions{}).HasError()

	if ma.Type.ValueString() == multiagentType20261001 {
		if !priorDecoded || pm.Type.ValueString() != multiagentType20261001 {
			pm = agentMultiagentModel{
				Subagents: types.ObjectNull(agentMultiagentMemberAttrTypes),
				Workflows: types.ObjectNull(agentMultiagentMemberAttrTypes),
			}
		}
		return buildMultiagent20261001Params(ctx, ma, pm)
	}

	var entries []agentMultiagentEntryModel
	diags.Append(ma.Agents.ElementsAs(ctx, &entries, false)...)
	if diags.HasError() {
		return out, diags
	}

	priorVersions := map[string]int64{}
	if priorDecoded && !pm.Agents.IsNull() && !pm.Agents.IsUnknown() {
		var pe []agentMultiagentEntryModel
		if !pm.Agents.ElementsAs(ctx, &pe, false).HasError() {
			priorVersions = priorAgentVersions(pe)
		}
	}

	coordinator := anthropic.BetaManagedAgentsMultiagentCoordinatorParams{
		Type:   anthropic.BetaManagedAgentsMultiagentCoordinatorParamsType(ma.Type.ValueString()),
		Agents: make([]anthropic.BetaManagedAgentsMultiagentRosterEntryParamsUnion, 0, len(entries)),
	}
	for _, e := range entries {
		switch e.Type.ValueString() {
		case multiagentEntryAgent:
			p := agentReferenceParams(e, priorVersions)
			coordinator.Agents = append(coordinator.Agents, anthropic.BetaManagedAgentsMultiagentRosterEntryParamsUnion{OfBetaManagedAgentsAgents: &p})
		case multiagentEntrySelf:
			coordinator.Agents = append(coordinator.Agents, anthropic.BetaManagedAgentsMultiagentRosterEntryParamsUnion{
				OfBetaManagedAgentsMultiagentSelfs: &anthropic.BetaManagedAgentsMultiagentSelfParams{
					Type: anthropic.BetaManagedAgentsMultiagentSelfParamsTypeSelf,
				},
			})
		case multiagentEntryAdvisor:
			coordinator.Agents = append(coordinator.Agents, anthropic.BetaManagedAgentsMultiagentRosterEntryParamsUnion{
				OfBetaManagedAgentsAdvisors: &anthropic.BetaManagedAgentsAdvisorParams{
					Model: e.Model.ValueString(),
					Type:  anthropic.BetaManagedAgentsAdvisorParamsTypeAdvisor,
				},
			})
		default:
			diags.AddError("Invalid multiagent roster entry", fmt.Sprintf("Unknown roster entry type %q.", e.Type.ValueString()))
			return out, diags
		}
	}
	out.OfCoordinator = &coordinator
	return out, diags
}

// priorAgentVersions returns the version state holds for each `agent` entry.
func priorAgentVersions(entries []agentMultiagentEntryModel) map[string]int64 {
	versions := map[string]int64{}
	for _, e := range entries {
		if e.Type.ValueString() == multiagentEntryAgent && !e.ID.IsNull() && !e.Version.IsNull() && !e.Version.IsUnknown() {
			versions[e.ID.ValueString()] = e.Version.ValueInt64()
		}
	}
	return versions
}

// agentReferenceParams builds an `agent` entry: the planned version when known,
// else the version prior state holds for the same id, else none.
func agentReferenceParams(e agentMultiagentEntryModel, priorVersions map[string]int64) anthropic.BetaManagedAgentsAgentParams {
	p := anthropic.BetaManagedAgentsAgentParams{
		ID:   e.ID.ValueString(),
		Type: anthropic.BetaManagedAgentsAgentParamsTypeAgent,
	}
	if !e.Version.IsNull() && !e.Version.IsUnknown() {
		p.Version = param.NewOpt(e.Version.ValueInt64())
	} else if e.Version.IsUnknown() {
		if v, ok := priorVersions[e.ID.ValueString()]; ok {
			p.Version = param.NewOpt(v)
		}
	}
	return p
}

// multiagentMemberParams is a `subagents` or `workflows` setting resolved to
// what is sent, its defaults filled in.
type multiagentMemberParams struct {
	enabled    bool
	inline     bool
	predefined []anthropic.BetaManagedAgentsMultiagentPredefinedAgentParamsUnion
}

// buildMultiagent20261001Params builds the multiagent_20261001 object. On an
// update that keeps this type, the API merges what is sent into the stored
// configuration level by level, and an omitted key keeps its stored value. So
// every key is sent, defaults included (an empty predefined_agents list, inline
// agents enabled, a disabled advisor): the stored configuration then always
// ends up exactly as planned, whatever it held before.
func buildMultiagent20261001Params(ctx context.Context, ma, prior agentMultiagentModel) (anthropic.BetaManagedAgentsMultiagentParamsUnion, diag.Diagnostics) {
	var diags diag.Diagnostics
	var out anthropic.BetaManagedAgentsMultiagentParamsUnion
	p := &anthropic.BetaManagedAgentsMultiagent20261001Params{}

	sub, d := buildMultiagentMember(ctx, ma.Subagents, prior.Subagents)
	diags.Append(d...)
	wf, d := buildMultiagentMember(ctx, ma.Workflows, prior.Workflows)
	diags.Append(d...)
	if diags.HasError() {
		return out, diags
	}

	if sub.enabled {
		p.Subagents.OfEnabled = &anthropic.BetaManagedAgentsMultiagentSubagentsEnabledParams{
			InlineAgents:     inlineAgentsParams(sub.inline),
			PredefinedAgents: sub.predefined,
		}
	} else {
		disabled := anthropic.NewBetaManagedAgentsMultiagentSubagentsDisabledParams()
		p.Subagents.OfDisabled = &disabled
	}
	if wf.enabled {
		p.Workflows.OfEnabled = &anthropic.BetaManagedAgentsMultiagentWorkflowsEnabledParams{
			InlineAgents:     inlineAgentsParams(wf.inline),
			PredefinedAgents: wf.predefined,
		}
	} else {
		disabled := anthropic.NewBetaManagedAgentsMultiagentWorkflowsDisabledParams()
		p.Workflows.OfDisabled = &disabled
	}

	if ma.Advisor.IsNull() || ma.Advisor.IsUnknown() {
		disabled := anthropic.NewBetaManagedAgentsMultiagentAdvisorDisabledParams()
		p.Advisor.OfDisabled = &disabled
	} else {
		var adv agentMultiagentAdvisorModel
		diags.Append(ma.Advisor.As(ctx, &adv, basetypes.ObjectAsOptions{})...)
		if diags.HasError() {
			return out, diags
		}
		p.Advisor = anthropic.BetaManagedAgentsMultiagentAdvisorParamsOfEnabled(adv.Model.ValueString())
	}

	out.OfMultiagent20261001 = p
	return out, diags
}

func inlineAgentsParams(enabled bool) anthropic.BetaManagedAgentsMultiagentInlineAgentsParamsUnion {
	var u anthropic.BetaManagedAgentsMultiagentInlineAgentsParamsUnion
	if enabled {
		e := anthropic.NewBetaManagedAgentsMultiagentInlineAgentsEnabledParams()
		u.OfEnabled = &e
	} else {
		d := anthropic.NewBetaManagedAgentsMultiagentInlineAgentsDisabledParams()
		u.OfDisabled = &d
	}
	return u
}

// buildMultiagentMember resolves a planned `subagents` or `workflows` setting.
// prior is the same setting in state, for the pinned versions.
func buildMultiagentMember(ctx context.Context, obj, prior types.Object) (multiagentMemberParams, diag.Diagnostics) {
	var diags diag.Diagnostics
	out := multiagentMemberParams{
		enabled:    true,
		inline:     true,
		predefined: []anthropic.BetaManagedAgentsMultiagentPredefinedAgentParamsUnion{},
	}
	if !isKnown(obj) {
		return out, diags
	}
	var m agentMultiagentMemberModel
	diags.Append(obj.As(ctx, &m, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return out, diags
	}
	if isKnown(m.Enabled) && !m.Enabled.ValueBool() {
		out.enabled = false
		return out, diags
	}
	if isKnown(m.InlineAgentsEnabled) {
		out.inline = m.InlineAgentsEnabled.ValueBool()
	}
	if !isKnown(m.PredefinedAgents) {
		return out, diags
	}
	entries, d := predefinedAgentEntries(ctx, m.PredefinedAgents)
	diags.Append(d...)
	if diags.HasError() {
		return out, diags
	}
	predefined, d := predefinedAgentsParams(entries, priorPredefinedVersions(ctx, prior))
	diags.Append(d...)
	out.predefined = append(out.predefined, predefined...)
	return out, diags
}

// priorPredefinedVersions returns the version state holds for each `agent`
// entry of a setting's predefined_agents, none when prior has no list or one
// that cannot be decoded.
func priorPredefinedVersions(ctx context.Context, prior types.Object) map[string]int64 {
	if !isKnown(prior) {
		return map[string]int64{}
	}
	var pm agentMultiagentMemberModel
	if prior.As(ctx, &pm, basetypes.ObjectAsOptions{}).HasError() || !isKnown(pm.PredefinedAgents) {
		return map[string]int64{}
	}
	pe, d := predefinedAgentEntries(ctx, pm.PredefinedAgents)
	if d.HasError() {
		return map[string]int64{}
	}
	return priorAgentVersions(pe)
}

// predefinedAgentsParams builds the predefined_agents entries, an `agent`
// entry reusing the version prior state pins (agentReferenceParams). It
// stops at an entry of unknown type, returning the entries before it.
func predefinedAgentsParams(entries []agentMultiagentEntryModel, priorVersions map[string]int64) ([]anthropic.BetaManagedAgentsMultiagentPredefinedAgentParamsUnion, diag.Diagnostics) {
	var diags diag.Diagnostics
	var out []anthropic.BetaManagedAgentsMultiagentPredefinedAgentParamsUnion
	for _, e := range entries {
		switch e.Type.ValueString() {
		case multiagentEntryAgent:
			p := agentReferenceParams(e, priorVersions)
			out = append(out, anthropic.BetaManagedAgentsMultiagentPredefinedAgentParamsUnion{OfBetaManagedAgentsAgents: &p})
		case multiagentEntrySelf:
			out = append(out, anthropic.BetaManagedAgentsMultiagentPredefinedAgentParamsUnion{
				OfBetaManagedAgentsMultiagentSelfs: &anthropic.BetaManagedAgentsMultiagentSelfParams{
					Type: anthropic.BetaManagedAgentsMultiagentSelfParamsTypeSelf,
				},
			})
		default:
			diags.AddError("Invalid predefined agent", fmt.Sprintf("Unknown predefined agent type %q.", e.Type.ValueString()))
			return out, diags
		}
	}
	return out, diags
}

// predefinedAgentEntries decodes a `predefined_agents` list into roster entries
// (with a null model), so the coordinator helpers serve both lists.
func predefinedAgentEntries(ctx context.Context, list types.List) ([]agentMultiagentEntryModel, diag.Diagnostics) {
	var predefined []agentMultiagentPredefinedAgentModel
	diags := list.ElementsAs(ctx, &predefined, false)
	if diags.HasError() {
		return nil, diags
	}
	entries := make([]agentMultiagentEntryModel, len(predefined))
	for i, p := range predefined {
		entries[i] = agentMultiagentEntryModel{Type: p.Type, ID: p.ID, Version: p.Version, Model: types.StringNull()}
	}
	return entries, diags
}

// multiagentClearField is the extra field that clears the roster on update:
// the typed struct field is omitted when zero, so a JSON null must be forced.
func multiagentClearField() map[string]any {
	return map[string]any{"multiagent": nil}
}

// --- Response mapping ---

func multiagentEntryValue(typ string, id types.String, version types.Int64, model types.String) (attr.Value, diag.Diagnostics) {
	return types.ObjectValue(agentMultiagentEntryAttrTypes, map[string]attr.Value{
		"type":    types.StringValue(typ),
		"id":      id,
		"version": version,
		"model":   model,
	})
}

func predefinedAgentValue(typ string, id types.String, version types.Int64) (attr.Value, diag.Diagnostics) {
	return types.ObjectValue(agentMultiagentPredefinedAgentAttrTypes, map[string]attr.Value{
		"type":    types.StringValue(typ),
		"id":      id,
		"version": version,
	})
}

// multiagentRoster is the API's coordinator object reduced to what the
// Terraform schema holds: a type and a flat list of agent and advisor entries.
type multiagentRoster struct {
	Type   string
	Agents []multiagentRosterEntry
}

type multiagentRosterEntry struct {
	Type    string
	ID      string
	Version int64
	Model   string
}

// multiagentRosterFromAPI flattens the coordinator branch of the API's
// multiagent union. present is false when the response carries no multiagent
// object (an absent or null field leaves the SDK union at its zero value).
// multiagent_20261001 never reaches it (see mapMultiagentToState). Any other
// type can only come from an agent configured outside Terraform with a type
// this provider version does not know: rather than failing the read, its type
// is kept with whatever roster it carries, and a warning names the agent.
func multiagentRosterFromAPI(ma anthropic.BetaManagedAgentsMultiagentUnion, ownerID string) (multiagentRoster, bool, diag.Diagnostics) {
	var diags diag.Diagnostics
	if len(ma.Agents) == 0 && ma.Type == "" {
		return multiagentRoster{}, false, diags
	}
	roster := multiagentRoster{Type: ma.Type}
	for _, a := range ma.Agents {
		roster.Agents = append(roster.Agents, multiagentRosterEntry{Type: a.Type, ID: a.ID, Version: a.Version, Model: a.Model})
	}
	if ma.Type != multiagentTypeCoordinator {
		diags.AddWarning("Unsupported multiagent type", fmt.Sprintf(
			"Agent %q has a multiagent configuration of type %q, which this provider version does not manage: only %q and %q are supported. "+
				"If this agent is managed by an anthropic_agent resource, the next apply replaces this configuration with the configured "+
				"\"multiagent\" block, or removes it when the block is absent.",
			ownerID, ma.Type, multiagentTypeCoordinator, multiagentType20261001))
	}
	return roster, true, diags
}

func multiagentAPIEntryValue(a multiagentRosterEntry) (attr.Value, diag.Diagnostics) {
	id, version, model := types.StringNull(), types.Int64Null(), types.StringNull()
	if a.ID != "" {
		id = types.StringValue(a.ID)
	}
	if a.Version != 0 {
		version = types.Int64Value(a.Version)
	}
	if a.Model != "" {
		model = types.StringValue(a.Model)
	}
	return multiagentEntryValue(a.Type, id, version, model)
}

func predefinedAgentAPIValue(a multiagentRosterEntry) (attr.Value, diag.Diagnostics) {
	id, version := types.StringNull(), types.Int64Null()
	if a.ID != "" {
		id = types.StringValue(a.ID)
	}
	if a.Version != 0 {
		version = types.Int64Value(a.Version)
	}
	return predefinedAgentValue(a.Type, id, version)
}

// mapMultiagentToObject maps the API configuration verbatim (no prior state;
// data sources). The API resolves a "self" entry into an "agent" entry carrying
// the owner's own ID, so a self entry cannot be told apart from an explicit
// reference here.
func mapMultiagentToObject(ma anthropic.BetaManagedAgentsMultiagentUnion, ownerID string) (types.Object, diag.Diagnostics) {
	if ma.Type == multiagentType20261001 {
		return mapMultiagent20261001(context.Background(), ma, ownerID, types.ObjectNull(agentMultiagentAttrTypes), true)
	}
	roster, present, diags := multiagentRosterFromAPI(ma, ownerID)
	if !present {
		return types.ObjectNull(agentMultiagentAttrTypes), diags
	}
	return mapMultiagentRosterToObject(roster, diags)
}

func mapMultiagentRosterToObject(roster multiagentRoster, diags diag.Diagnostics) (types.Object, diag.Diagnostics) {
	vals := make([]attr.Value, 0, len(roster.Agents))
	for _, a := range roster.Agents {
		v, d := multiagentAPIEntryValue(a)
		diags.Append(d...)
		vals = append(vals, v)
	}
	return multiagentObject(roster.Type, vals, diags)
}

// multiagentObject builds a coordinator-shaped object: the roster in `agents`,
// the multiagent_20261001 settings null.
func multiagentObject(typ string, vals []attr.Value, diags diag.Diagnostics) (types.Object, diag.Diagnostics) {
	list, d := types.ListValue(types.ObjectType{AttrTypes: agentMultiagentEntryAttrTypes}, vals)
	diags.Append(d...)
	obj, d := types.ObjectValue(agentMultiagentAttrTypes, map[string]attr.Value{
		"type":      types.StringValue(typ),
		"agents":    list,
		"subagents": types.ObjectNull(agentMultiagentMemberAttrTypes),
		"workflows": types.ObjectNull(agentMultiagentMemberAttrTypes),
		"advisor":   types.ObjectNull(agentMultiagentAdvisorAttrTypes),
	})
	diags.Append(d...)
	return obj, diags
}

// mapMultiagentToState maps the API configuration onto Terraform state,
// correlating with the planned/prior configuration. For the coordinator roster,
// config order and entry kinds survive the API's normalisation: "self" comes
// back as an "agent" reference to the owner and the "advisor" entry is echoed
// last (see correlateRoster). multiagent_20261001 goes through
// mapMultiagent20261001. Without a prior roster (import, first read) the API
// roster is mapped verbatim.
func mapMultiagentToState(ctx context.Context, ma anthropic.BetaManagedAgentsMultiagentUnion, ownerID string, prior types.Object) (types.Object, diag.Diagnostics) {
	if ma.Type == multiagentType20261001 {
		return mapMultiagent20261001(ctx, ma, ownerID, prior, false)
	}
	roster, present, rosterDiags := multiagentRosterFromAPI(ma, ownerID)
	if !present {
		return types.ObjectNull(agentMultiagentAttrTypes), rosterDiags
	}
	if prior.IsNull() || prior.IsUnknown() {
		return mapMultiagentRosterToObject(roster, rosterDiags)
	}
	// A prior roster that cannot be decoded falls back to the verbatim mapping
	// without surfacing the decoding error.
	var diags diag.Diagnostics
	var pm agentMultiagentModel
	diags.Append(prior.As(ctx, &pm, basetypes.ObjectAsOptions{})...)
	if diags.HasError() || pm.Agents.IsNull() || pm.Agents.IsUnknown() {
		return mapMultiagentRosterToObject(roster, rosterDiags)
	}
	var priorEntries []agentMultiagentEntryModel
	diags.Append(pm.Agents.ElementsAs(ctx, &priorEntries, false)...)
	if diags.HasError() {
		return mapMultiagentRosterToObject(roster, rosterDiags)
	}
	diags.Append(rosterDiags...)

	vals, d := correlateRoster(priorEntries, roster.Agents, ownerID, multiagentAPIEntryValue, func() (attr.Value, diag.Diagnostics) {
		return multiagentEntryValue(multiagentEntrySelf, types.StringNull(), types.Int64Null(), types.StringNull())
	})
	diags.Append(d...)
	return multiagentObject(roster.Type, vals, diags)
}

// correlateRoster maps API entries onto the prior (planned or state) entries
// by key, never by index:
//   - agent: the API agent entry with the same id;
//   - advisor: the API advisor entry;
//   - self: the API agent entry whose id is ownerID, not claimed by an explicit agent entry.
//
// Prior entries with no API counterpart are dropped (the plan then shows the
// diff), and API entries claimed by no prior entry are appended with their API
// type (drift). apiValue and selfValue build the entry objects, so the
// coordinator roster and the predefined_agents lists share the algorithm.
func correlateRoster(priorEntries []agentMultiagentEntryModel, apiEntries []multiagentRosterEntry, ownerID string,
	apiValue func(multiagentRosterEntry) (attr.Value, diag.Diagnostics), selfValue func() (attr.Value, diag.Diagnostics),
) ([]attr.Value, diag.Diagnostics) {
	c := rosterCorrelation{api: apiEntries, used: make([]bool, len(apiEntries)), slots: make([]attr.Value, len(priorEntries))}
	// Pass 1: explicit agents and advisors. Pass 2: self, so an explicit reference
	// to the owner's own ID is never claimed by a self entry.
	for pass := 1; pass <= 2; pass++ {
		for i, p := range priorEntries {
			if match := rosterMatch(pass, p, ownerID); match != nil {
				c.claim(i, p.Type.ValueString(), match, apiValue, selfValue)
			}
		}
	}

	vals := make([]attr.Value, 0, len(apiEntries))
	for _, s := range c.slots {
		if s != nil {
			vals = append(vals, s)
		}
	}
	for i, a := range apiEntries {
		if c.used[i] {
			continue
		}
		v, d := apiValue(a)
		c.diags.Append(d...)
		vals = append(vals, v)
	}
	return vals, c.diags
}

// rosterCorrelation is the progress of correlateRoster: the API entries
// already claimed, and the value of each prior entry that found one.
type rosterCorrelation struct {
	api   []multiagentRosterEntry
	used  []bool
	slots []attr.Value
	diags diag.Diagnostics
}

// claim gives prior entry i, of type typ, the first unclaimed API entry match
// accepts, if any: the API entry's value, or the self value for a self entry.
func (c *rosterCorrelation) claim(i int, typ string, match func(multiagentRosterEntry) bool,
	apiValue func(multiagentRosterEntry) (attr.Value, diag.Diagnostics), selfValue func() (attr.Value, diag.Diagnostics),
) {
	for idx, a := range c.api {
		if c.used[idx] || !match(a) {
			continue
		}
		c.used[idx] = true
		var v attr.Value
		var d diag.Diagnostics
		if typ == multiagentEntrySelf {
			v, d = selfValue()
		} else {
			v, d = apiValue(a)
		}
		c.diags.Append(d...)
		c.slots[i] = v
		return
	}
}

// rosterMatch returns what identifies the API entry of prior entry p in pass
// (1: agent by id, advisor by type; 2: self as the owner's own agent entry),
// or nil when p is not correlated in that pass.
func rosterMatch(pass int, p agentMultiagentEntryModel, ownerID string) func(multiagentRosterEntry) bool {
	switch typ := p.Type.ValueString(); {
	case pass == 1 && typ == multiagentEntryAgent:
		return func(a multiagentRosterEntry) bool {
			return a.Type == multiagentEntryAgent && a.ID == p.ID.ValueString()
		}
	case pass == 1 && typ == multiagentEntryAdvisor:
		return func(a multiagentRosterEntry) bool { return a.Type == multiagentEntryAdvisor }
	case pass == 2 && typ == multiagentEntrySelf:
		return func(a multiagentRosterEntry) bool {
			return a.Type == multiagentEntryAgent && a.ID == ownerID
		}
	}
	return nil
}

// multiagentMemberAPI is a `subagents` or `workflows` setting as the API
// returns it. The SDK types the two as distinct unions with the same fields.
type multiagentMemberAPI struct {
	enabled bool
	inline  bool
	agents  []multiagentRosterEntry
}

// isDefault reports whether the setting holds the API defaults.
func (m multiagentMemberAPI) isDefault() bool {
	return m.enabled && m.inline && len(m.agents) == 0
}

func newMultiagentMemberAPI(typ, inlineType string, refs []anthropic.BetaManagedAgentsAgentReference) multiagentMemberAPI {
	m := multiagentMemberAPI{enabled: typ != multiagentMemberDisabled, inline: inlineType != multiagentMemberDisabled}
	for _, r := range refs {
		m.agents = append(m.agents, multiagentRosterEntry{Type: string(r.Type), ID: r.ID, Version: r.Version})
	}
	return m
}

// mapMultiagent20261001 maps a multiagent_20261001 configuration. With
// verbatim (data sources) every setting is mapped as the API returns it. For
// the resource, a field the configuration leaves unset (current: plan or prior
// state) stays null while the API value equals its default, and a whole
// setting the configuration leaves unset stays null while it holds only
// defaults, so an echoed default causes no diff and anything else shows up as
// drift, which the next apply reverts. predefined_agents correlate with the
// configured entries like the coordinator roster.
func mapMultiagent20261001(ctx context.Context, ma anthropic.BetaManagedAgentsMultiagentUnion, ownerID string, current types.Object, verbatim bool) (types.Object, diag.Diagnostics) {
	var diags diag.Diagnostics
	cur := agentMultiagentModel{
		Subagents: types.ObjectNull(agentMultiagentMemberAttrTypes),
		Workflows: types.ObjectNull(agentMultiagentMemberAttrTypes),
	}
	if !current.IsNull() && !current.IsUnknown() {
		var decoded agentMultiagentModel
		if !current.As(ctx, &decoded, basetypes.ObjectAsOptions{}).HasError() {
			cur = decoded
		}
	}

	sub, d := mapMultiagentMember(ctx,
		newMultiagentMemberAPI(ma.Subagents.Type, ma.Subagents.InlineAgents.Type, ma.Subagents.PredefinedAgents),
		cur.Subagents, ownerID, verbatim)
	diags.Append(d...)
	wf, d := mapMultiagentMember(ctx,
		newMultiagentMemberAPI(ma.Workflows.Type, ma.Workflows.InlineAgents.Type, ma.Workflows.PredefinedAgents),
		cur.Workflows, ownerID, verbatim)
	diags.Append(d...)

	advisor := types.ObjectNull(agentMultiagentAdvisorAttrTypes)
	if ma.Advisor.Type == multiagentMemberEnabled {
		obj, d := types.ObjectValue(agentMultiagentAdvisorAttrTypes, map[string]attr.Value{"model": types.StringValue(ma.Advisor.Model)})
		diags.Append(d...)
		advisor = obj
	}

	obj, d := types.ObjectValue(agentMultiagentAttrTypes, map[string]attr.Value{
		"type":      types.StringValue(multiagentType20261001),
		"agents":    types.ListNull(types.ObjectType{AttrTypes: agentMultiagentEntryAttrTypes}),
		"subagents": sub,
		"workflows": wf,
		"advisor":   advisor,
	})
	diags.Append(d...)
	return obj, diags
}

func mapMultiagentMember(ctx context.Context, api multiagentMemberAPI, current types.Object, ownerID string, verbatim bool) (types.Object, diag.Diagnostics) {
	if verbatim {
		return mapMultiagentMemberVerbatim(api)
	}

	var diags diag.Diagnostics
	configured := isKnown(current)
	if !configured && api.isDefault() {
		return types.ObjectNull(agentMultiagentMemberAttrTypes), diags
	}
	cur := agentMultiagentMemberModel{
		Enabled:             types.BoolNull(),
		InlineAgentsEnabled: types.BoolNull(),
		PredefinedAgents:    types.ListNull(types.ObjectType{AttrTypes: agentMultiagentPredefinedAgentAttrTypes}),
	}
	if configured {
		var decoded agentMultiagentMemberModel
		if d := current.As(ctx, &decoded, basetypes.ObjectAsOptions{}); !d.HasError() {
			cur = decoded
		}
	}

	enabled := types.BoolValue(api.enabled)
	if api.enabled && cur.Enabled.IsNull() {
		enabled = types.BoolNull()
	}
	inline := types.BoolNull()
	predefined := types.ListNull(types.ObjectType{AttrTypes: agentMultiagentPredefinedAgentAttrTypes})
	if api.enabled {
		if !api.inline || !cur.InlineAgentsEnabled.IsNull() {
			inline = types.BoolValue(api.inline)
		}
		var d diag.Diagnostics
		predefined, d = correlatePredefinedAgents(ctx, api.agents, cur.PredefinedAgents, ownerID)
		diags.Append(d...)
	}

	obj, d := types.ObjectValue(agentMultiagentMemberAttrTypes, map[string]attr.Value{
		"enabled": enabled, "inline_agents_enabled": inline, "predefined_agents": predefined,
	})
	diags.Append(d...)
	return obj, diags
}

// mapMultiagentMemberVerbatim maps a setting as the API returns it (data
// sources): inline_agents_enabled and predefined_agents null when the setting
// is off.
func mapMultiagentMemberVerbatim(api multiagentMemberAPI) (types.Object, diag.Diagnostics) {
	var diags diag.Diagnostics
	inline := types.BoolNull()
	predefined := types.ListNull(types.ObjectType{AttrTypes: agentMultiagentPredefinedAgentAttrTypes})
	if api.enabled {
		inline = types.BoolValue(api.inline)
		var d diag.Diagnostics
		predefined, d = predefinedAgentsVerbatim(api.agents)
		diags.Append(d...)
	}
	obj, d := types.ObjectValue(agentMultiagentMemberAttrTypes, map[string]attr.Value{
		"enabled": types.BoolValue(api.enabled), "inline_agents_enabled": inline, "predefined_agents": predefined,
	})
	diags.Append(d...)
	return obj, diags
}

// predefinedAgentsVerbatim maps the API's predefined agents as returned, null
// when there are none.
func predefinedAgentsVerbatim(agents []multiagentRosterEntry) (types.List, diag.Diagnostics) {
	predefinedType := types.ObjectType{AttrTypes: agentMultiagentPredefinedAgentAttrTypes}
	if len(agents) == 0 {
		return types.ListNull(predefinedType), nil
	}
	var diags diag.Diagnostics
	vals := make([]attr.Value, 0, len(agents))
	for _, a := range agents {
		v, d := predefinedAgentAPIValue(a)
		diags.Append(d...)
		vals = append(vals, v)
	}
	list, d := types.ListValue(predefinedType, vals)
	diags.Append(d...)
	return list, diags
}

// correlatePredefinedAgents maps the API's predefined agents onto the
// configured list like the coordinator roster (correlateRoster); verbatim when
// the configuration has no list or one that cannot be decoded.
func correlatePredefinedAgents(ctx context.Context, agents []multiagentRosterEntry, current types.List, ownerID string) (types.List, diag.Diagnostics) {
	if !isKnown(current) {
		return predefinedAgentsVerbatim(agents)
	}
	priorEntries, d := predefinedAgentEntries(ctx, current)
	if d.HasError() {
		return predefinedAgentsVerbatim(agents)
	}
	vals, diags := correlateRoster(priorEntries, agents, ownerID, predefinedAgentAPIValue, func() (attr.Value, diag.Diagnostics) {
		return predefinedAgentValue(multiagentEntrySelf, types.StringNull(), types.Int64Null())
	})
	list, d := types.ListValue(types.ObjectType{AttrTypes: agentMultiagentPredefinedAgentAttrTypes}, vals)
	diags.Append(d...)
	return list, diags
}
