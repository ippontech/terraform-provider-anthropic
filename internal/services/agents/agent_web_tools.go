// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
)

// Web options of an `agent_toolset.configs` entry
// (https://platform.claude.com/docs/en/managed-agents/tools-web-restrictions).
// `url_sources` (web_fetch) is not in SDK v1.67.0 and is not exposed yet.

const (
	toolNameWebFetch  = "web_fetch"
	toolNameWebSearch = "web_search"
)

// webToolOptionTools lists, per web option attribute, the built-in tools that
// accept it. The plan-time validator and buildAgentToolConfigParams both read it.
var webToolOptionTools = map[string][]string{
	"allowed_domains":    {toolNameWebFetch, toolNameWebSearch},
	"blocked_domains":    {toolNameWebFetch, toolNameWebSearch},
	"max_content_tokens": {toolNameWebFetch},
	"user_location":      {toolNameWebSearch},
}

// webToolOptionRejection returns a diagnostic detail when the built-in tool
// `name` does not accept `option`, or "" when it does.
func webToolOptionRejection(option, name string) string {
	tools := webToolOptionTools[option]
	if slices.Contains(tools, name) {
		return ""
	}
	quoted := make([]string, len(tools))
	for i, t := range tools {
		quoted[i] = fmt.Sprintf("%q", t)
	}
	return fmt.Sprintf("%q is only allowed when name is %s, not %q.", option, strings.Join(quoted, " or "), name)
}

type agentUserLocationModel struct {
	Country  types.String `tfsdk:"country"`
	City     types.String `tfsdk:"city"`
	Region   types.String `tfsdk:"region"`
	Timezone types.String `tfsdk:"timezone"`
}

var agentUserLocationAttrTypes = map[string]attr.Type{
	"country":  types.StringType,
	"city":     types.StringType,
	"region":   types.StringType,
	"timezone": types.StringType,
}

// --- Config validation ---

// agentToolConfigsValidator enforces the rules of the web options on
// `agent_toolset.configs` entries that depend on more than one attribute.
type agentToolConfigsValidator struct{}

func (v *agentToolConfigsValidator) Description(_ context.Context) string {
	return "Validates the web options of agent_toolset.configs entries: allowed_domains and blocked_domains are mutually exclusive " +
		"and apply to web_fetch and web_search only, max_content_tokens applies to web_fetch only and user_location to web_search only."
}

func (v *agentToolConfigsValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v *agentToolConfigsValidator) ValidateResource(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var obj types.Object
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("agent_toolset"), &obj)...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(validateAgentToolConfigs(ctx, obj)...)
}

// validateAgentToolConfigs checks the web options of each configs entry. Unknown
// values count as neither set nor missing, so configs referencing not-yet-known
// values stay valid at plan time; an unknown entry or name is skipped.
func validateAgentToolConfigs(ctx context.Context, toolset types.Object) diag.Diagnostics {
	var diags diag.Diagnostics
	if toolset.IsNull() || toolset.IsUnknown() {
		return diags
	}
	configs, ok := toolset.Attributes()["configs"].(types.List)
	if !ok || configs.IsNull() || configs.IsUnknown() {
		return diags
	}

	isSet := func(v attr.Value) bool { return !v.IsNull() && !v.IsUnknown() }

	// Decode element by element: ElementsAs fails on a single unknown element.
	for i, el := range configs.Elements() {
		obj, ok := el.(types.Object)
		if !ok || obj.IsNull() || obj.IsUnknown() {
			continue
		}
		var c agentToolConfigModel
		if d := obj.As(ctx, &c, basetypes.ObjectAsOptions{}); d.HasError() {
			diags.Append(d...)
			return diags
		}
		p := path.Root("agent_toolset").AtName("configs").AtListIndex(i)

		if isSet(c.AllowedDomains) && isSet(c.BlockedDomains) {
			diags.AddAttributeError(p.AtName("blocked_domains"), "Conflicting domain lists",
				`Set either "allowed_domains" or "blocked_domains" on a tool, not both.`)
		}

		if isSet(c.UserLocation) {
			var loc agentUserLocationModel
			if d := c.UserLocation.As(ctx, &loc, basetypes.ObjectAsOptions{}); d.HasError() {
				diags.Append(d...)
				return diags
			}
			// An unknown field may still resolve to a value: only an all-null
			// object is rejected.
			if loc.Country.IsNull() && loc.City.IsNull() && loc.Region.IsNull() && loc.Timezone.IsNull() {
				diags.AddAttributeError(p.AtName("user_location"), "Empty user location",
					`"user_location" must set at least one of "country", "city", "region" or "timezone".`)
			}
		}

		if c.Name.IsNull() || c.Name.IsUnknown() {
			continue
		}
		for _, opt := range []struct {
			option string
			value  attr.Value
		}{
			{"allowed_domains", c.AllowedDomains},
			{"blocked_domains", c.BlockedDomains},
			{"max_content_tokens", c.MaxContentTokens},
			{"user_location", c.UserLocation},
		} {
			if !isSet(opt.value) {
				continue
			}
			if detail := webToolOptionRejection(opt.option, c.Name.ValueString()); detail != "" {
				diags.AddAttributeError(p.AtName(opt.option), "Unsupported tool option", detail)
			}
		}
	}
	return diags
}

// --- Plan -> params ---

// webToolOptions carries the web options of one `configs` entry to
// buildAgentToolConfigParams. The zero value sets none of them.
type webToolOptions struct {
	allowedDomains   []string
	blockedDomains   []string
	maxContentTokens param.Opt[int64]
	userLocation     *anthropic.BetaManagedAgentsUserLocationParam
}

// rejectedFor returns one diagnostic detail per option set on o that the
// built-in tool `name` does not accept. agentToolConfigsValidator rejects them
// at plan time; this is the apply-time net for a name unknown at plan time.
func (o webToolOptions) rejectedFor(name string) []string {
	var details []string
	for _, opt := range []struct {
		option string
		set    bool
	}{
		{"allowed_domains", o.allowedDomains != nil},
		{"blocked_domains", o.blockedDomains != nil},
		{"max_content_tokens", o.maxContentTokens.Valid()},
		{"user_location", o.userLocation != nil},
	} {
		if !opt.set {
			continue
		}
		if detail := webToolOptionRejection(opt.option, name); detail != "" {
			details = append(details, detail)
		}
	}
	return details
}

// webToolOptionsFromModel reads the web options of a planned `configs` entry.
func webToolOptionsFromModel(ctx context.Context, c agentToolConfigModel) (webToolOptions, diag.Diagnostics) {
	var diags diag.Diagnostics
	var o webToolOptions

	if !c.AllowedDomains.IsNull() && !c.AllowedDomains.IsUnknown() {
		o.allowedDomains = []string{}
		diags.Append(c.AllowedDomains.ElementsAs(ctx, &o.allowedDomains, false)...)
	}
	if !c.BlockedDomains.IsNull() && !c.BlockedDomains.IsUnknown() {
		o.blockedDomains = []string{}
		diags.Append(c.BlockedDomains.ElementsAs(ctx, &o.blockedDomains, false)...)
	}
	if !c.MaxContentTokens.IsNull() && !c.MaxContentTokens.IsUnknown() {
		o.maxContentTokens = param.NewOpt(c.MaxContentTokens.ValueInt64())
	}
	if !c.UserLocation.IsNull() && !c.UserLocation.IsUnknown() {
		var loc agentUserLocationModel
		diags.Append(c.UserLocation.As(ctx, &loc, basetypes.ObjectAsOptions{})...)
		// `type` is set explicitly: the SDK elides a zero-valued location
		// (omitzero), which would silently drop a configured object.
		p := &anthropic.BetaManagedAgentsUserLocationParam{Type: constant.ValueOf[constant.Approximate]()}
		for _, f := range []struct {
			value types.String
			dst   *param.Opt[string]
		}{
			{loc.Country, &p.Country},
			{loc.City, &p.City},
			{loc.Region, &p.Region},
			{loc.Timezone, &p.Timezone},
		} {
			if !f.value.IsNull() && !f.value.IsUnknown() {
				*f.dst = param.NewOpt(f.value.ValueString())
			}
		}
		o.userLocation = p
	}
	return o, diags
}

// --- Response -> state ---

// webToolOptionsFromResponse maps the web options of a built-in tool config
// verbatim. The API documents no default for them: an unset option comes back
// absent or null and maps to null, so unlike `enabled` and `permission_policy`
// no comparison with an inherited default is needed to avoid a perpetual diff,
// and a value set out of band surfaces as drift. Tools other than web_fetch and
// web_search never carry them and map to nulls. For a config state or plan
// tracks, webToolOptionsToState then resolves these values against it.
func webToolOptionsFromResponse(c anthropic.BetaManagedAgentsAgentToolConfigUnion) (map[string]attr.Value, diag.Diagnostics) {
	var diags diag.Diagnostics

	domains := func(field []string, present bool) types.Set {
		if !present || len(field) == 0 {
			return types.SetNull(types.StringType)
		}
		elems := make([]attr.Value, len(field))
		for i, d := range field {
			elems[i] = types.StringValue(d)
		}
		set, d := types.SetValue(types.StringType, elems)
		diags.Append(d...)
		return set
	}

	maxContentTokens := types.Int64Null()
	if c.JSON.MaxContentTokens.Valid() {
		maxContentTokens = types.Int64Value(c.MaxContentTokens)
	}

	userLocation := types.ObjectNull(agentUserLocationAttrTypes)
	if c.JSON.UserLocation.Valid() {
		obj, d := types.ObjectValue(agentUserLocationAttrTypes, map[string]attr.Value{
			"country":  tfvalue.StringOrNull(c.UserLocation.Country),
			"city":     tfvalue.StringOrNull(c.UserLocation.City),
			"region":   tfvalue.StringOrNull(c.UserLocation.Region),
			"timezone": tfvalue.StringOrNull(c.UserLocation.Timezone),
		})
		diags.Append(d...)
		userLocation = obj
	}

	return map[string]attr.Value{
		"allowed_domains":    domains(c.AllowedDomains, c.JSON.AllowedDomains.Valid()),
		"blocked_domains":    domains(c.BlockedDomains, c.JSON.BlockedDomains.Valid()),
		"max_content_tokens": maxContentTokens,
		"user_location":      userLocation,
	}, diags
}

// webToolOptionsToState resolves the web options the API returns for a config
// entry current tracks (its attributes in the plan on Create and Update, in the
// prior state on Read). Neither the echo of the domain entries nor the absence
// of defaults is verified against the live API, so only a real difference
// reaches state:
//   - a domain set that equals current's the way the API compares entries
//     (hostnames ignore case and a trailing "/") keeps current's spelling, in
//     case the API stores entries normalized;
//   - with applyMapping, an option current leaves null stays null, and so does
//     a user_location field: a default the API resolved for it would otherwise
//     fail the apply as an inconsistent result. It shows on the next Read.
//
// Any other API value is kept, so a change made outside Terraform shows as
// drift on Read.
func webToolOptionsToState(current, api map[string]attr.Value, mode mappingMode) map[string]attr.Value {
	resolved := make(map[string]attr.Value, len(api))
	for option, apiValue := range api {
		cur, ok := current[option]
		switch {
		case !ok || cur.IsUnknown():
			resolved[option] = apiValue
		case cur.IsNull() && mode == applyMapping:
			resolved[option] = cur
		case option == "allowed_domains" || option == "blocked_domains":
			resolved[option] = domainSetToState(cur, apiValue)
		case option == "user_location" && mode == applyMapping:
			resolved[option] = userLocationToState(cur, apiValue)
		default:
			resolved[option] = apiValue
		}
	}
	return resolved
}

// domainKey is a domain entry as the API compares it: hostnames ignore case
// and a trailing "/".
func domainKey(entry string) string {
	return strings.ToLower(strings.TrimRight(entry, "/"))
}

// domainSetToState returns current when api holds the same entries under
// domainKey, and api otherwise.
func domainSetToState(current, api attr.Value) attr.Value {
	cur, okCur := current.(types.Set)
	got, okAPI := api.(types.Set)
	if !okCur || !okAPI || cur.IsNull() || got.IsNull() || len(cur.Elements()) != len(got.Elements()) {
		return api
	}
	keys := func(s types.Set) map[string]bool {
		k := make(map[string]bool, len(s.Elements()))
		for _, el := range s.Elements() {
			str, ok := el.(types.String)
			if !ok || str.IsNull() || str.IsUnknown() {
				return nil
			}
			k[domainKey(str.ValueString())] = true
		}
		return k
	}
	// Entries current repeats under domainKey make the match ambiguous.
	want, have := keys(cur), keys(got)
	if want == nil || have == nil || len(want) != len(cur.Elements()) || !maps.Equal(want, have) {
		return api
	}
	return current
}

// userLocationToState keeps null the user_location fields the plan leaves
// null (applyMapping only).
func userLocationToState(current, api attr.Value) attr.Value {
	cur, okCur := current.(types.Object)
	got, okAPI := api.(types.Object)
	if !okCur || !okAPI || cur.IsNull() || got.IsNull() {
		return api
	}
	fields := maps.Clone(got.Attributes())
	for name, value := range cur.Attributes() {
		if value.IsNull() {
			fields[name] = value
		}
	}
	obj, diags := types.ObjectValue(agentUserLocationAttrTypes, fields)
	if diags.HasError() {
		return api
	}
	return obj
}

// apiAgentToolConfig converts a built-in tool config from the API into the
// shape mapToolConfigsToState correlates, with its web options as verbatim
// extra attributes.
func apiAgentToolConfig(c anthropic.BetaManagedAgentsAgentToolConfigUnion) (apiToolConfig, diag.Diagnostics) {
	extra, diags := webToolOptionsFromResponse(c)
	return apiToolConfig{
		name:             c.Name,
		enabled:          c.Enabled,
		permissionPolicy: c.PermissionPolicy.Type,
		extra:            extra,
	}, diags
}

// agentToolConfigObject maps a built-in tool config verbatim (data sources).
func agentToolConfigObject(c anthropic.BetaManagedAgentsAgentToolConfigUnion) (types.Object, diag.Diagnostics) {
	cfg, diags := apiAgentToolConfig(c)
	obj, d := types.ObjectValue(agentToolConfigAttrTypes, cfg.values(types.BoolValue(cfg.enabled), types.StringValue(cfg.permissionPolicy)))
	diags.Append(d...)
	return obj, diags
}
