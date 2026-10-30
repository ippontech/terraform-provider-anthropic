// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents

import (
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/hashicorp/terraform-plugin-framework/diag"
)

// builtinTool is a built-in tool of agent_toolset_20260401
// (https://platform.claude.com/docs/en/api/beta/agents/create, tools[].configs).
// Since SDK v1.66.0 the tool-config union has one branch per tool, so the
// Terraform `name` attribute selects the branch config fills.
type builtinTool struct {
	name string
	// options are the web options of a configs entry the tool accepts
	// (agent_web_tools.go).
	options []string
	config  func(c builtinToolConfig) anthropic.BetaManagedAgentsAgentToolConfigParamsUnion
}

// builtinToolConfig is a configs entry resolved to what the request carries.
// policyType "" leaves the permission policy unset, so the toolset default
// applies.
type builtinToolConfig struct {
	enabled    param.Opt[bool]
	policyType string
	web        webToolOptions
}

// policy returns the variants of a tool's PermissionPolicyUnion to fill (see
// permissionPolicyParams).
func (c builtinToolConfig) policy() (*anthropic.BetaManagedAgentsAlwaysAllowPolicyParam, *anthropic.BetaManagedAgentsAlwaysAskPolicyParam, *anthropic.BetaManagedAgentsAutoPolicyParam) {
	return permissionPolicyParams(c.policyType)
}

// builtinTools is the one list of the built-in tools: the `name` validator of
// agent_toolset.configs, the schema description, the web options each tool
// accepts (webToolOptionTools) and the request (buildAgentToolConfigParams)
// all read it, so a new tool is one entry here.
var builtinTools = []builtinTool{
	{name: "bash", config: func(c builtinToolConfig) anthropic.BetaManagedAgentsAgentToolConfigParamsUnion {
		cfg := &anthropic.BetaManagedAgentsBashToolConfigParams{Enabled: c.enabled}
		cfg.PermissionPolicy.OfAlwaysAllow, cfg.PermissionPolicy.OfAlwaysAsk, cfg.PermissionPolicy.OfAuto = c.policy()
		return anthropic.BetaManagedAgentsAgentToolConfigParamsUnion{OfBash: cfg}
	}},
	{name: "edit", config: func(c builtinToolConfig) anthropic.BetaManagedAgentsAgentToolConfigParamsUnion {
		cfg := &anthropic.BetaManagedAgentsEditToolConfigParams{Enabled: c.enabled}
		cfg.PermissionPolicy.OfAlwaysAllow, cfg.PermissionPolicy.OfAlwaysAsk, cfg.PermissionPolicy.OfAuto = c.policy()
		return anthropic.BetaManagedAgentsAgentToolConfigParamsUnion{OfEdit: cfg}
	}},
	{name: "read", config: func(c builtinToolConfig) anthropic.BetaManagedAgentsAgentToolConfigParamsUnion {
		cfg := &anthropic.BetaManagedAgentsReadToolConfigParams{Enabled: c.enabled}
		cfg.PermissionPolicy.OfAlwaysAllow, cfg.PermissionPolicy.OfAlwaysAsk, cfg.PermissionPolicy.OfAuto = c.policy()
		return anthropic.BetaManagedAgentsAgentToolConfigParamsUnion{OfRead: cfg}
	}},
	{name: "write", config: func(c builtinToolConfig) anthropic.BetaManagedAgentsAgentToolConfigParamsUnion {
		cfg := &anthropic.BetaManagedAgentsWriteToolConfigParams{Enabled: c.enabled}
		cfg.PermissionPolicy.OfAlwaysAllow, cfg.PermissionPolicy.OfAlwaysAsk, cfg.PermissionPolicy.OfAuto = c.policy()
		return anthropic.BetaManagedAgentsAgentToolConfigParamsUnion{OfWrite: cfg}
	}},
	{name: "glob", config: func(c builtinToolConfig) anthropic.BetaManagedAgentsAgentToolConfigParamsUnion {
		cfg := &anthropic.BetaManagedAgentsGlobToolConfigParams{Enabled: c.enabled}
		cfg.PermissionPolicy.OfAlwaysAllow, cfg.PermissionPolicy.OfAlwaysAsk, cfg.PermissionPolicy.OfAuto = c.policy()
		return anthropic.BetaManagedAgentsAgentToolConfigParamsUnion{OfGlob: cfg}
	}},
	{name: "grep", config: func(c builtinToolConfig) anthropic.BetaManagedAgentsAgentToolConfigParamsUnion {
		cfg := &anthropic.BetaManagedAgentsGrepToolConfigParams{Enabled: c.enabled}
		cfg.PermissionPolicy.OfAlwaysAllow, cfg.PermissionPolicy.OfAlwaysAsk, cfg.PermissionPolicy.OfAuto = c.policy()
		return anthropic.BetaManagedAgentsAgentToolConfigParamsUnion{OfGrep: cfg}
	}},
	{
		name:    toolNameWebFetch,
		options: []string{"allowed_domains", "blocked_domains", "max_content_tokens", "url_sources"},
		config:  webFetchToolConfig,
	},
	{
		name:    toolNameWebSearch,
		options: []string{"allowed_domains", "blocked_domains", "user_location"},
		config:  webSearchToolConfig,
	},
}

func webFetchToolConfig(c builtinToolConfig) anthropic.BetaManagedAgentsAgentToolConfigParamsUnion {
	cfg := &anthropic.BetaManagedAgentsWebFetchToolConfigParams{
		Enabled:          c.enabled,
		AllowedDomains:   c.web.allowedDomains,
		BlockedDomains:   c.web.blockedDomains,
		MaxContentTokens: c.web.maxContentTokens,
	}
	if c.web.urlSources != nil {
		cfg.URLSources = *c.web.urlSources
	}
	cfg.PermissionPolicy.OfAlwaysAllow, cfg.PermissionPolicy.OfAlwaysAsk, cfg.PermissionPolicy.OfAuto = c.policy()
	return anthropic.BetaManagedAgentsAgentToolConfigParamsUnion{OfWebFetch: cfg}
}

func webSearchToolConfig(c builtinToolConfig) anthropic.BetaManagedAgentsAgentToolConfigParamsUnion {
	cfg := &anthropic.BetaManagedAgentsWebSearchToolConfigParams{
		Enabled:        c.enabled,
		AllowedDomains: c.web.allowedDomains,
		BlockedDomains: c.web.blockedDomains,
	}
	if c.web.userLocation != nil {
		cfg.UserLocation = *c.web.userLocation
	}
	cfg.PermissionPolicy.OfAlwaysAllow, cfg.PermissionPolicy.OfAlwaysAsk, cfg.PermissionPolicy.OfAuto = c.policy()
	return anthropic.BetaManagedAgentsAgentToolConfigParamsUnion{OfWebSearch: cfg}
}

// builtinToolNames returns the names of the built-in tools, in table order.
func builtinToolNames() []string {
	names := make([]string, len(builtinTools))
	for i, t := range builtinTools {
		names[i] = t.name
	}
	return names
}

// builtinToolOptions returns, per web option, the built-in tools that accept
// it, in table order.
func builtinToolOptions() map[string][]string {
	byOption := map[string][]string{}
	for _, t := range builtinTools {
		for _, o := range t.options {
			byOption[o] = append(byOption[o], t.name)
		}
	}
	return byOption
}

// buildAgentToolConfigParams maps a `configs` entry to the branch of the SDK's
// tool-config union its name selects in builtinTools. The web options only
// exist on the web_fetch and web_search branches; one set for a tool that does
// not take it is an error rather than silently dropped. A name builtinTools
// does not hold is an error too: the schema validator rejects it at plan time,
// but a name unknown then is only checked here, mid-apply.
func buildAgentToolConfigParams(name string, enabled param.Opt[bool], policyType string, web webToolOptions) (anthropic.BetaManagedAgentsAgentToolConfigParamsUnion, diag.Diagnostics) {
	var diags diag.Diagnostics
	var u anthropic.BetaManagedAgentsAgentToolConfigParamsUnion

	if rejected := web.rejectedFor(name); len(rejected) > 0 {
		diags.AddError("Unsupported tool option", strings.Join(rejected, " "))
		return u, diags
	}

	for _, t := range builtinTools {
		if t.name == name {
			return t.config(builtinToolConfig{enabled: enabled, policyType: policyType, web: web}), diags
		}
	}
	diags.AddError(
		"Unsupported built-in tool name",
		fmt.Sprintf("%q is not a supported built-in tool name. Expected one of: %s.", name, strings.Join(builtinToolNames(), ", ")),
	)
	return u, diags
}
