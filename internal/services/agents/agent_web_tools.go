// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/anthropics/anthropic-sdk-go/packages/respjson"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
	"github.com/hashicorp/terraform-plugin-framework-validators/setvalidator"
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
	"github.com/ippontech/terraform-provider-anthropic/internal/tfvalue"
)

// Web options of an `agent_toolset.configs` entry
// (https://platform.claude.com/docs/en/managed-agents/tools-web-restrictions).
// `url_sources` (web_fetch, SDK v1.79.0+) is described in the agent create
// reference (https://platform.claude.com/docs/en/api/beta/agents/create) and
// in https://platform.claude.com/docs/en/agents-and-tools/tool-use/web-fetch-tool#url-sources.

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
	"url_sources":        {toolNameWebFetch},
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

// URL source values. `user_input` takes the first two only; the tool result
// filters take all four, `only` and `except` with a `tools` list.
const (
	urlSourceAll    = "all"
	urlSourceNone   = "none"
	urlSourceOnly   = "only"
	urlSourceExcept = "except"
)

// agentURLSourcesModel is the `url_sources` object of a web_fetch config.
type agentURLSourcesModel struct {
	UserInput         types.String `tfsdk:"user_input"`
	ClientToolResults types.Object `tfsdk:"client_tool_results"`
	ServerToolResults types.Object `tfsdk:"server_tool_results"`
}

// agentURLSourceFilterModel is a tool result filter of `url_sources`.
type agentURLSourceFilterModel struct {
	Type  types.String `tfsdk:"type"`
	Tools types.Set    `tfsdk:"tools"`
}

var agentURLSourceFilterAttrTypes = map[string]attr.Type{
	"type":  types.StringType,
	"tools": types.SetType{ElemType: types.StringType},
}

var agentURLSourcesAttrTypes = map[string]attr.Type{
	"user_input":          types.StringType,
	"client_tool_results": types.ObjectType{AttrTypes: agentURLSourceFilterAttrTypes},
	"server_tool_results": types.ObjectType{AttrTypes: agentURLSourceFilterAttrTypes},
}

// urlSourcesSchema is the `url_sources` attribute of an `agent_toolset.configs`
// entry.
func urlSourcesSchema() schema.SingleNestedAttribute {
	filter := func(source, toolsRule string, toolValidators ...validator.String) schema.SingleNestedAttribute {
		return schema.SingleNestedAttribute{
			Optional: true,
			MarkdownDescription: "Which " + source + " contribute URLs that may be fetched. " +
				"Omit it to allow every one (the API default).",
			Attributes: map[string]schema.Attribute{
				"type": schema.StringAttribute{
					Required: true,
					MarkdownDescription: "`all` (every result), `none` (no result), `only` (the results of the tools in `tools`) " +
						"or `except` (every result but those of the tools in `tools`).",
					Validators: []validator.String{stringvalidator.OneOf(urlSourceAll, urlSourceNone, urlSourceOnly, urlSourceExcept)},
				},
				"tools": schema.SetAttribute{
					Optional:    true,
					ElementType: types.StringType,
					MarkdownDescription: "Tool names, compared case-sensitively. Required when `type` is `only` or `except`, " +
						"forbidden otherwise. 1-128 names. " + toolsRule,
					Validators: []validator.Set{
						setvalidator.SizeBetween(1, 128),
						setvalidator.ValueStringsAre(append([]validator.String{stringvalidator.LengthBetween(1, 128)}, toolValidators...)...),
					},
				},
			},
		}
	}
	return schema.SingleNestedAttribute{
		Optional: true,
		MarkdownDescription: "`web_fetch` only. Which sources may contribute the URLs the tool fetches: the tool only fetches a URL " +
			"that appeared earlier in the session, in a user message, a custom tool result or a `web_search` or `web_fetch` result, " +
			"and each key below narrows one of those sources (a URL planted in a tool result is a prompt-injection path). " +
			"Omitted keys allow their source. At least one key must be set, and setting all three to `none` is rejected.",
		Attributes: map[string]schema.Attribute{
			"user_input": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Whether URLs in the text of user messages may be fetched: `all` or `none`.",
				Validators:          []validator.String{stringvalidator.OneOf(urlSourceAll, urlSourceNone)},
			},
			"client_tool_results": filter("custom tools' results",
				"Each name must be a tool in `custom_tools`; with `except`, a custom tool added later counts as a source, with `only` it does not."),
			"server_tool_results": filter("`web_search` and `web_fetch` results",
				"Each name must be `web_search` or `web_fetch`.",
				stringvalidator.OneOf(toolNameWebSearch, toolNameWebFetch)),
		},
	}
}

// urlSourcesDataSourceSchema is the read-only `url_sources` attribute of the
// data sources.
func urlSourcesDataSourceSchema() dsschema.SingleNestedAttribute {
	filter := func(source string) dsschema.SingleNestedAttribute {
		return dsschema.SingleNestedAttribute{
			Computed:            true,
			MarkdownDescription: "Which " + source + " contribute URLs that may be fetched, null when every one does.",
			Attributes: map[string]dsschema.Attribute{
				"type":  dsschema.StringAttribute{Computed: true, MarkdownDescription: "`all`, `none`, `only` or `except`."},
				"tools": dsschema.SetAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Tool names of an `only` or `except` filter."},
			},
		}
	}
	return dsschema.SingleNestedAttribute{
		Computed:            true,
		MarkdownDescription: "Which sources may contribute the URLs `web_fetch` fetches, null when every source may.",
		Attributes: map[string]dsschema.Attribute{
			"user_input":          dsschema.StringAttribute{Computed: true, MarkdownDescription: "Whether URLs in user messages may be fetched: `all` or `none`, null when unset."},
			"client_tool_results": filter("custom tools' results"),
			"server_tool_results": filter("`web_search` and `web_fetch` results"),
		},
	}
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

		if isSet(c.URLSources) {
			diags.Append(validateURLSources(ctx, c.URLSources, p.AtName("url_sources"))...)
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
			{"url_sources", c.URLSources},
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

// validateURLSources checks the rules of a known `url_sources` object the
// schema cannot express: at least one key set, not all three `none`, and a
// `tools` list exactly when a filter's type is `only` or `except`. Unknown
// values count as neither set nor missing.
func validateURLSources(ctx context.Context, obj types.Object, p path.Path) diag.Diagnostics {
	var diags diag.Diagnostics
	var src agentURLSourcesModel
	if d := obj.As(ctx, &src, basetypes.ObjectAsOptions{}); d.HasError() {
		return d
	}

	if src.UserInput.IsNull() && src.ClientToolResults.IsNull() && src.ServerToolResults.IsNull() {
		diags.AddAttributeError(p, "Empty URL sources",
			`"url_sources" must set at least one of "user_input", "client_tool_results" or "server_tool_results"; omit it to allow every source.`)
		return diags
	}

	// allNone stays true only while every key is known to be "none".
	allNone := src.UserInput.ValueString() == urlSourceNone
	for _, f := range []struct {
		key   string
		value types.Object
	}{
		{"client_tool_results", src.ClientToolResults},
		{"server_tool_results", src.ServerToolResults},
	} {
		if f.value.IsNull() || f.value.IsUnknown() {
			allNone = false
			continue
		}
		var filter agentURLSourceFilterModel
		if d := f.value.As(ctx, &filter, basetypes.ObjectAsOptions{}); d.HasError() {
			diags.Append(d...)
			return diags
		}
		allNone = allNone && filter.Type.ValueString() == urlSourceNone
		if filter.Type.IsNull() || filter.Type.IsUnknown() {
			continue
		}
		withTools := filter.Type.ValueString() == urlSourceOnly || filter.Type.ValueString() == urlSourceExcept
		switch {
		case withTools && filter.Tools.IsNull():
			diags.AddAttributeError(p.AtName(f.key).AtName("tools"), "Missing tools",
				fmt.Sprintf(`"tools" is required when "type" is %q.`, filter.Type.ValueString()))
		case !withTools && !filter.Tools.IsNull() && !filter.Tools.IsUnknown():
			diags.AddAttributeError(p.AtName(f.key).AtName("tools"), "Unexpected tools",
				fmt.Sprintf(`"tools" is only allowed when "type" is "only" or "except", not %q.`, filter.Type.ValueString()))
		}
	}

	if allNone {
		diags.AddAttributeError(p, "No URL source left",
			`Setting "user_input", "client_tool_results" and "server_tool_results" all to "none" is rejected by the API: `+
				`disable web_fetch instead (enabled = false).`)
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
	urlSources       *anthropic.BetaManagedAgentsWebFetchURLSourcesParams
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
		{"url_sources", o.urlSources != nil},
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
	if !c.URLSources.IsNull() && !c.URLSources.IsUnknown() {
		src, d := urlSourcesParamsFromModel(ctx, c.URLSources)
		diags.Append(d...)
		o.urlSources = src
	}
	return o, diags
}

// urlSourcesParamsFromModel builds the `url_sources` request object. Every key
// is sent in its object form ({"type": ...}), the form the API answers in;
// an unset key is omitted, which allows its source.
func urlSourcesParamsFromModel(ctx context.Context, obj types.Object) (*anthropic.BetaManagedAgentsWebFetchURLSourcesParams, diag.Diagnostics) {
	var diags diag.Diagnostics
	var src agentURLSourcesModel
	diags.Append(obj.As(ctx, &src, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return nil, diags
	}

	p := &anthropic.BetaManagedAgentsWebFetchURLSourcesParams{}
	if !src.UserInput.IsNull() && !src.UserInput.IsUnknown() {
		u := &anthropic.BetaManagedAgentsWebFetchURLSourceUserInputUnionParam{}
		switch src.UserInput.ValueString() {
		case urlSourceAll:
			all := anthropic.NewBetaManagedAgentsWebFetchURLSourceAllParam()
			u.OfAll = &all
		case urlSourceNone:
			none := anthropic.NewBetaManagedAgentsWebFetchURLSourceNoneParam()
			u.OfNone = &none
		default:
			diags.AddError("Invalid url_sources", fmt.Sprintf(`"user_input" must be "all" or "none", not %q.`, src.UserInput.ValueString()))
			return nil, diags
		}
		p.UserInput.OfBetaManagedAgentsWebFetchURLSourceUserInputUnion = u
	}
	for _, f := range []struct {
		key   string
		value types.Object
		dst   *anthropic.BetaManagedAgentsWebFetchURLSourceToolFilterParamsUnion
	}{
		{"client_tool_results", src.ClientToolResults, &p.ClientToolResults},
		{"server_tool_results", src.ServerToolResults, &p.ServerToolResults},
	} {
		if f.value.IsNull() || f.value.IsUnknown() {
			continue
		}
		filter, d := urlSourceFilterParamFromModel(ctx, f.key, f.value)
		diags.Append(d...)
		if diags.HasError() {
			return nil, diags
		}
		f.dst.OfBetaManagedAgentsWebFetchURLSourceToolFilterUnion = filter
	}
	return p, diags
}

func urlSourceFilterParamFromModel(ctx context.Context, key string, obj types.Object) (*anthropic.BetaManagedAgentsWebFetchURLSourceToolFilterUnionParam, diag.Diagnostics) {
	var diags diag.Diagnostics
	var filter agentURLSourceFilterModel
	diags.Append(obj.As(ctx, &filter, basetypes.ObjectAsOptions{})...)
	if diags.HasError() {
		return nil, diags
	}
	var names []string
	if !filter.Tools.IsNull() && !filter.Tools.IsUnknown() {
		diags.Append(filter.Tools.ElementsAs(ctx, &names, false)...)
		if diags.HasError() {
			return nil, diags
		}
	}
	refs := make([]anthropic.BetaManagedAgentsWebFetchURLSourceToolReferenceParam, len(names))
	for i, n := range names {
		refs[i] = anthropic.BetaManagedAgentsWebFetchURLSourceToolReferenceParam{Name: n}
	}

	u := &anthropic.BetaManagedAgentsWebFetchURLSourceToolFilterUnionParam{}
	switch filter.Type.ValueString() {
	case urlSourceAll:
		all := anthropic.NewBetaManagedAgentsWebFetchURLSourceAllParam()
		u.OfAll = &all
	case urlSourceNone:
		none := anthropic.NewBetaManagedAgentsWebFetchURLSourceNoneParam()
		u.OfNone = &none
	case urlSourceOnly:
		u.OfOnly = &anthropic.BetaManagedAgentsWebFetchURLSourceOnlyParam{Tools: refs}
	case urlSourceExcept:
		u.OfExcept = &anthropic.BetaManagedAgentsWebFetchURLSourceExceptParam{Tools: refs}
	default:
		diags.AddError("Invalid url_sources", fmt.Sprintf(`%q type must be "all", "none", "only" or "except", not %q.`, key, filter.Type.ValueString()))
		return nil, diags
	}
	return u, diags
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

	urlSources, d := urlSourcesFromResponse(c)
	diags.Append(d...)

	return map[string]attr.Value{
		"allowed_domains":    domains(c.AllowedDomains, c.JSON.AllowedDomains.Valid()),
		"blocked_domains":    domains(c.BlockedDomains, c.JSON.BlockedDomains.Valid()),
		"max_content_tokens": maxContentTokens,
		"user_location":      userLocation,
		"url_sources":        urlSources,
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

// urlSourcesFromResponse maps `url_sources` verbatim. The API answers in the
// object form ({"type": ...}) and null for an unset key or object; a key given
// in the string shorthand ("all"/"none") is read as well, so it never maps to
// null by accident.
func urlSourcesFromResponse(c anthropic.BetaManagedAgentsAgentToolConfigUnion) (types.Object, diag.Diagnostics) {
	var diags diag.Diagnostics
	if !c.JSON.URLSources.Valid() {
		return types.ObjectNull(agentURLSourcesAttrTypes), diags
	}
	src := c.URLSources

	filter := func(f anthropic.BetaManagedAgentsWebFetchURLSourceToolFilterUnion, field respjson.Field) types.Object {
		typ := urlSourceType(f.Type, field)
		if typ.IsNull() {
			return types.ObjectNull(agentURLSourceFilterAttrTypes)
		}
		tools := types.SetNull(types.StringType)
		if len(f.Tools) > 0 {
			elems := make([]attr.Value, len(f.Tools))
			for i, t := range f.Tools {
				elems[i] = types.StringValue(t.Name)
			}
			set, d := types.SetValue(types.StringType, elems)
			diags.Append(d...)
			tools = set
		}
		obj, d := types.ObjectValue(agentURLSourceFilterAttrTypes, map[string]attr.Value{"type": typ, "tools": tools})
		diags.Append(d...)
		return obj
	}

	obj, d := types.ObjectValue(agentURLSourcesAttrTypes, map[string]attr.Value{
		"user_input":          urlSourceType(src.UserInput.Type, src.JSON.UserInput),
		"client_tool_results": filter(src.ClientToolResults, src.JSON.ClientToolResults),
		"server_tool_results": filter(src.ServerToolResults, src.JSON.ServerToolResults),
	})
	diags.Append(d...)
	return obj, diags
}

// urlSourceType returns the type of a url_sources key: the decoded object's
// `type`, the string itself when the key came in the shorthand form, or null
// when the key is absent or null.
func urlSourceType(decoded string, field respjson.Field) types.String {
	if field.Valid() && decoded != "" {
		return types.StringValue(decoded)
	}
	var shorthand string
	if raw := field.Raw(); raw != "" && json.Unmarshal([]byte(raw), &shorthand) == nil && shorthand != "" {
		return types.StringValue(shorthand)
	}
	return types.StringNull()
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
