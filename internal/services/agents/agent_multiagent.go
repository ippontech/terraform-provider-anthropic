// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
)

// Roster entry types.
const (
	multiagentEntryAgent   = "agent"
	multiagentEntrySelf    = "self"
	multiagentEntryAdvisor = "advisor"
)

type agentMultiagentModel struct {
	Type   types.String `tfsdk:"type"`
	Agents types.List   `tfsdk:"agents"`
}

type agentMultiagentEntryModel struct {
	Type    types.String `tfsdk:"type"`
	ID      types.String `tfsdk:"id"`
	Version types.Int64  `tfsdk:"version"`
	Model   types.String `tfsdk:"model"`
}

var agentMultiagentEntryAttrTypes = map[string]attr.Type{
	"type":    types.StringType,
	"id":      types.StringType,
	"version": types.Int64Type,
	"model":   types.StringType,
}

var agentMultiagentAttrTypes = map[string]attr.Type{
	"type":   types.StringType,
	"agents": types.ListType{ElemType: types.ObjectType{AttrTypes: agentMultiagentEntryAttrTypes}},
}

// --- Config validation ---

// multiagentConfigValidator enforces the cross-attribute rules of the roster.
type multiagentConfigValidator struct{}

func (v *multiagentConfigValidator) Description(_ context.Context) string {
	return "Validates the multiagent roster: id for agent entries, model for advisor entries, at most one self and one advisor."
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

// validateMultiagentConfig checks the roster rules. Unknown values count as neither
// set nor missing, so configs referencing not-yet-known values stay valid at plan time.
func validateMultiagentConfig(ctx context.Context, obj types.Object) diag.Diagnostics {
	var diags diag.Diagnostics
	if obj.IsNull() || obj.IsUnknown() {
		return diags
	}
	var ma agentMultiagentModel
	diags.Append(obj.As(ctx, &ma, basetypes.ObjectAsOptions{})...)
	if diags.HasError() || ma.Agents.IsNull() || ma.Agents.IsUnknown() {
		return diags
	}
	var entries []agentMultiagentEntryModel
	diags.Append(ma.Agents.ElementsAs(ctx, &entries, false)...)
	if diags.HasError() {
		return diags
	}

	isSet := func(v attr.Value) bool { return !v.IsNull() && !v.IsUnknown() }
	isMissing := func(v attr.Value) bool { return v.IsNull() && !v.IsUnknown() }

	selfs, advisors := 0, 0
	for i, e := range entries {
		p := path.Root("multiagent").AtName("agents").AtListIndex(i)
		if e.Type.IsNull() || e.Type.IsUnknown() {
			continue
		}
		switch e.Type.ValueString() {
		case multiagentEntryAgent:
			if isMissing(e.ID) {
				diags.AddAttributeError(p.AtName("id"), "Missing roster agent ID", `"id" is required when type is "agent".`)
			}
			if isSet(e.Model) {
				diags.AddAttributeError(p.AtName("model"), "Unexpected roster model", `"model" is only allowed when type is "advisor".`)
			}
		case multiagentEntrySelf:
			selfs++
			if isSet(e.ID) {
				diags.AddAttributeError(p.AtName("id"), "Unexpected roster agent ID", `"id" is only allowed when type is "agent".`)
			}
			if isSet(e.Model) {
				diags.AddAttributeError(p.AtName("model"), "Unexpected roster model", `"model" is only allowed when type is "advisor".`)
			}
			if isSet(e.Version) {
				diags.AddAttributeError(p.AtName("version"), "Unexpected roster version", `"version" is only allowed when type is "agent".`)
			}
		case multiagentEntryAdvisor:
			advisors++
			if isMissing(e.Model) {
				diags.AddAttributeError(p.AtName("model"), "Missing advisor model", `"model" is required when type is "advisor".`)
			}
			if isSet(e.ID) {
				diags.AddAttributeError(p.AtName("id"), "Unexpected roster agent ID", `"id" is only allowed when type is "agent".`)
			}
			if isSet(e.Version) {
				diags.AddAttributeError(p.AtName("version"), "Unexpected roster version", `"version" is only allowed when type is "agent".`)
			}
		}
	}
	if selfs > 1 {
		diags.AddAttributeError(path.Root("multiagent").AtName("agents"), "Duplicate self entry", `At most one roster entry may have type "self".`)
	}
	if advisors > 1 {
		diags.AddAttributeError(path.Root("multiagent").AtName("agents"), "Duplicate advisor entry", `At most one roster entry may have type "advisor".`)
	}
	return diags
}

// --- Request building ---

// buildMultiagentParams converts the planned multiagent object into SDK params.
// It returns the zero value (omitted by the SDK) for a null or unknown object.
func buildMultiagentParams(ctx context.Context, obj types.Object) (anthropic.BetaManagedAgentsMultiagentParams, diag.Diagnostics) {
	var diags diag.Diagnostics
	var out anthropic.BetaManagedAgentsMultiagentParams
	if obj.IsNull() || obj.IsUnknown() {
		return out, diags
	}
	var ma agentMultiagentModel
	diags.Append(obj.As(ctx, &ma, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return out, diags
	}
	var entries []agentMultiagentEntryModel
	diags.Append(ma.Agents.ElementsAs(ctx, &entries, false)...)
	if diags.HasError() {
		return out, diags
	}

	out.Type = anthropic.BetaManagedAgentsMultiagentParamsType(ma.Type.ValueString())
	out.Agents = make([]anthropic.BetaManagedAgentsMultiagentRosterEntryParamsUnion, 0, len(entries))
	for _, e := range entries {
		switch e.Type.ValueString() {
		case multiagentEntryAgent:
			p := anthropic.BetaManagedAgentsAgentParams{
				ID:   e.ID.ValueString(),
				Type: anthropic.BetaManagedAgentsAgentParamsTypeAgent,
			}
			if !e.Version.IsNull() && !e.Version.IsUnknown() {
				p.Version = param.NewOpt(e.Version.ValueInt64())
			}
			out.Agents = append(out.Agents, anthropic.BetaManagedAgentsMultiagentRosterEntryParamsUnion{OfBetaManagedAgentsAgents: &p})
		case multiagentEntrySelf:
			out.Agents = append(out.Agents, anthropic.BetaManagedAgentsMultiagentRosterEntryParamsUnion{
				OfBetaManagedAgentsMultiagentSelfs: &anthropic.BetaManagedAgentsMultiagentSelfParams{
					Type: anthropic.BetaManagedAgentsMultiagentSelfParamsTypeSelf,
				},
			})
		case multiagentEntryAdvisor:
			out.Agents = append(out.Agents, anthropic.BetaManagedAgentsMultiagentRosterEntryParamsUnion{
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
	return out, diags
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

func multiagentAPIEntryValue(a anthropic.BetaManagedAgentsMultiagentAgentUnion) (attr.Value, diag.Diagnostics) {
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

// multiagentPresent reports whether the API response carries a roster. An absent or
// null field leaves the SDK struct at its zero value.
func multiagentPresent(ma anthropic.BetaManagedAgentsMultiagent) bool {
	return len(ma.Agents) > 0 || ma.Type != ""
}

// mapMultiagentToObject maps the API roster verbatim (no prior state). The API
// resolves a "self" entry into an "agent" entry carrying the owner's own ID, so a
// self entry cannot be told apart from an explicit reference here.
func mapMultiagentToObject(ma anthropic.BetaManagedAgentsMultiagent) (types.Object, diag.Diagnostics) {
	var diags diag.Diagnostics
	if !multiagentPresent(ma) {
		return types.ObjectNull(agentMultiagentAttrTypes), diags
	}
	vals := make([]attr.Value, 0, len(ma.Agents))
	for _, a := range ma.Agents {
		v, d := multiagentAPIEntryValue(a)
		diags.Append(d...)
		vals = append(vals, v)
	}
	return multiagentObject(string(ma.Type), vals, diags)
}

func multiagentObject(typ string, vals []attr.Value, diags diag.Diagnostics) (types.Object, diag.Diagnostics) {
	list, d := types.ListValue(types.ObjectType{AttrTypes: agentMultiagentEntryAttrTypes}, vals)
	diags.Append(d...)
	obj, d := types.ObjectValue(agentMultiagentAttrTypes, map[string]attr.Value{
		"type":   types.StringValue(typ),
		"agents": list,
	})
	diags.Append(d...)
	return obj, diags
}

// mapMultiagentToState maps the API roster onto Terraform state, correlating with the
// planned/prior roster so that config order and entry kinds survive the API's
// normalisation: "self" comes back as an "agent" reference to the owner and the
// "advisor" entry is echoed last. Correlation is by key, never by index:
//   - agent: the API agent entry with the same id;
//   - advisor: the API advisor entry;
//   - self: the API agent entry whose id is ownerID, not claimed by an explicit agent entry.
//
// Prior entries with no API counterpart are dropped (the plan then shows the diff),
// and API entries claimed by no prior entry are appended with their API type (drift).
// Without a prior roster (import, first read) the API roster is mapped verbatim.
func mapMultiagentToState(ctx context.Context, ma anthropic.BetaManagedAgentsMultiagent, ownerID string, prior types.Object) (types.Object, diag.Diagnostics) {
	var diags diag.Diagnostics
	if !multiagentPresent(ma) {
		return types.ObjectNull(agentMultiagentAttrTypes), diags
	}
	if prior.IsNull() || prior.IsUnknown() {
		return mapMultiagentToObject(ma)
	}
	var pm agentMultiagentModel
	diags.Append(prior.As(ctx, &pm, basetypes.ObjectAsOptions{})...)
	if diags.HasError() || pm.Agents.IsNull() || pm.Agents.IsUnknown() {
		return mapMultiagentToObject(ma)
	}
	var priorEntries []agentMultiagentEntryModel
	diags.Append(pm.Agents.ElementsAs(ctx, &priorEntries, false)...)
	if diags.HasError() {
		return mapMultiagentToObject(ma)
	}

	used := make([]bool, len(ma.Agents))
	find := func(match func(a anthropic.BetaManagedAgentsMultiagentAgentUnion) bool) int {
		for i, a := range ma.Agents {
			if !used[i] && match(a) {
				used[i] = true
				return i
			}
		}
		return -1
	}

	slots := make([]attr.Value, len(priorEntries))
	// Pass 1: explicit agents and advisors. Pass 2: self, so an explicit reference
	// to the owner's own ID is never claimed by a self entry.
	for pass := 1; pass <= 2; pass++ {
		for i, p := range priorEntries {
			typ := p.Type.ValueString()
			var idx int
			switch {
			case pass == 1 && typ == multiagentEntryAgent:
				idx = find(func(a anthropic.BetaManagedAgentsMultiagentAgentUnion) bool {
					return a.Type == multiagentEntryAgent && a.ID == p.ID.ValueString()
				})
			case pass == 1 && typ == multiagentEntryAdvisor:
				idx = find(func(a anthropic.BetaManagedAgentsMultiagentAgentUnion) bool { return a.Type == multiagentEntryAdvisor })
			case pass == 2 && typ == multiagentEntrySelf:
				idx = find(func(a anthropic.BetaManagedAgentsMultiagentAgentUnion) bool {
					return a.Type == multiagentEntryAgent && a.ID == ownerID
				})
			default:
				continue
			}
			if idx < 0 {
				continue
			}
			var v attr.Value
			var d diag.Diagnostics
			if typ == multiagentEntrySelf {
				v, d = multiagentEntryValue(multiagentEntrySelf, types.StringNull(), types.Int64Null(), types.StringNull())
			} else {
				v, d = multiagentAPIEntryValue(ma.Agents[idx])
			}
			diags.Append(d...)
			slots[i] = v
		}
	}

	vals := make([]attr.Value, 0, len(ma.Agents))
	for _, s := range slots {
		if s != nil {
			vals = append(vals, s)
		}
	}
	for i, a := range ma.Agents {
		if used[i] {
			continue
		}
		v, d := multiagentAPIEntryValue(a)
		diags.Append(d...)
		vals = append(vals, v)
	}
	return multiagentObject(string(ma.Type), vals, diags)
}
