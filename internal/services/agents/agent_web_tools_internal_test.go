// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/ippontech/terraform-provider-anthropic/internal/schematest"
)

// toolCfg returns a configs entry for name with every other attribute null.
func toolCfg(name string) agentToolConfigModel {
	return agentToolConfigModel{
		Name:             types.StringValue(name),
		Enabled:          types.BoolNull(),
		PermissionPolicy: types.StringNull(),
		AllowedDomains:   types.SetNull(types.StringType),
		BlockedDomains:   types.SetNull(types.StringType),
		MaxContentTokens: types.Int64Null(),
		UserLocation:     types.ObjectNull(agentUserLocationAttrTypes),
		URLSources:       types.ObjectNull(agentURLSourcesAttrTypes),
	}
}

// urlFilter returns a url_sources tool filter; no tools leaves `tools` null.
func urlFilter(typ string, tools ...string) types.Object {
	toolSet := types.SetNull(types.StringType)
	if len(tools) > 0 {
		toolSet = strSet(tools...)
	}
	return types.ObjectValueMust(agentURLSourceFilterAttrTypes, map[string]attr.Value{
		"type": types.StringValue(typ), "tools": toolSet,
	})
}

func urlSources(userInput types.String, client, server types.Object) types.Object {
	return types.ObjectValueMust(agentURLSourcesAttrTypes, map[string]attr.Value{
		"user_input": userInput, "client_tool_results": client, "server_tool_results": server,
	})
}

var noURLFilter = types.ObjectNull(agentURLSourceFilterAttrTypes)

func strSet(values ...string) types.Set {
	elems := make([]attr.Value, len(values))
	for i, v := range values {
		elems[i] = types.StringValue(v)
	}
	return types.SetValueMust(types.StringType, elems)
}

func userLocation(country, city, region, timezone types.String) types.Object {
	return types.ObjectValueMust(agentUserLocationAttrTypes, map[string]attr.Value{
		"country": country, "city": city, "region": region, "timezone": timezone,
	})
}

// agentToolsetObj builds an agent_toolset object holding configs, which may
// also be unknown elements.
func agentToolsetObj(t *testing.T, configs ...attr.Value) types.Object {
	t.Helper()
	list, diags := types.ListValue(types.ObjectType{AttrTypes: agentToolConfigAttrTypes}, configs)
	if diags.HasError() {
		t.Fatalf("configs list: %+v", diags)
	}
	obj, diags := types.ObjectValue(agentToolsetAttrTypes, map[string]attr.Value{
		"default_enabled":           types.BoolNull(),
		"default_permission_policy": types.StringNull(),
		"configs":                   list,
	})
	if diags.HasError() {
		t.Fatalf("toolset: %+v", diags)
	}
	return obj
}

func toolCfgObj(t *testing.T, c agentToolConfigModel) attr.Value {
	t.Helper()
	obj, diags := types.ObjectValueFrom(context.Background(), agentToolConfigAttrTypes, c)
	if diags.HasError() {
		t.Fatalf("config object: %+v", diags)
	}
	return obj
}

func agentToolConfigsFromToolset(t *testing.T, toolset types.Object) []agentToolConfigModel {
	t.Helper()
	var ts agentToolsetModel
	if diags := toolset.As(context.Background(), &ts, basetypes.ObjectAsOptions{}); diags.HasError() {
		t.Fatalf("read agent_toolset: %+v", diags)
	}
	var configs []agentToolConfigModel
	if diags := ts.Configs.ElementsAs(context.Background(), &configs, false); diags.HasError() {
		t.Fatalf("read configs: %+v", diags)
	}
	return configs
}

// The web options reach the request on the web_fetch and web_search branches
// of the union, with `type = approximate` on the location.
func TestBuildToolsParams_webOptions(t *testing.T) {
	t.Parallel()

	search := toolCfg("web_search")
	search.AllowedDomains = strSet("docs.example.com")
	search.UserLocation = userLocation(types.StringValue("US"), types.StringNull(), types.StringNull(), types.StringValue("America/Los_Angeles"))
	fetch := toolCfg("web_fetch")
	fetch.PermissionPolicy = types.StringValue("always_ask")
	fetch.BlockedDomains = strSet("ads.example.com")
	fetch.MaxContentTokens = types.Int64Value(50000)

	data := AgentResourceModel{
		AgentToolset: agentToolsetObj(t, toolCfgObj(t, search), toolCfgObj(t, fetch), toolCfgObj(t, toolCfg("bash"))),
		MCPToolsets:  types.ListNull(types.ObjectType{AttrTypes: agentMCPToolsetAttrTypes}),
		CustomTools:  types.ListNull(types.ObjectType{AttrTypes: agentCustomToolAttrTypes}),
	}
	tools, diags := buildToolsParams(context.Background(), data)
	if diags.HasError() {
		t.Fatalf("diags: %+v", diags)
	}
	raw, err := json.Marshal(tools[0].OfAgentToolset20260401.Configs)
	if err != nil {
		t.Fatal(err)
	}
	const want = `[` +
		`{"allowed_domains":["docs.example.com"],"user_location":{"country":"US","timezone":"America/Los_Angeles","type":"approximate"},"name":"web_search"},` +
		`{"max_content_tokens":50000,"permission_policy":{"type":"always_ask"},"blocked_domains":["ads.example.com"],"name":"web_fetch"},` +
		`{"name":"bash"}]`
	if string(raw) != want {
		t.Fatalf("got  %s\nwant %s", raw, want)
	}
}

// url_sources reaches the web_fetch branch with every key in its object form:
// a shorthand key as {"type": ...} and a filter's tools as tool_reference
// objects. An unset key is omitted.
func TestBuildAgentToolConfigParams_urlSources(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		sources types.Object
		want    string
	}{
		{
			name:    "every key",
			sources: urlSources(types.StringValue("none"), urlFilter("except", "run_code"), urlFilter("only", "web_search")),
			want: `{"user_input":{"type":"none"},` +
				`"client_tool_results":{"type":"except","tools":[{"name":"run_code","type":"tool_reference"}]},` +
				`"server_tool_results":{"type":"only","tools":[{"name":"web_search","type":"tool_reference"}]}}`,
		},
		{
			name:    "one key",
			sources: urlSources(types.StringNull(), urlFilter("none"), noURLFilter),
			want:    `{"client_tool_results":{"type":"none"}}`,
		},
		{
			name:    "explicit all",
			sources: urlSources(types.StringValue("all"), noURLFilter, urlFilter("all")),
			want:    `{"user_input":{"type":"all"},"server_tool_results":{"type":"all"}}`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := toolCfg("web_fetch")
			c.URLSources = tc.sources
			web, diags := webToolOptionsFromModel(context.Background(), c)
			if diags.HasError() {
				t.Fatalf("diags: %+v", diags)
			}
			u, diags := buildAgentToolConfigParams("web_fetch", param.Opt[bool]{}, "", web)
			if diags.HasError() {
				t.Fatalf("diags: %+v", diags)
			}
			raw, err := json.Marshal(u)
			if err != nil {
				t.Fatal(err)
			}
			var got struct {
				URLSources json.RawMessage `json:"url_sources"`
			}
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			assertJSONEqual(t, string(got.URLSources), tc.want)
		})
	}
}

// A user_location whose fields are all null is still sent as an object: the
// SDK would otherwise elide the zero value (omitzero) and drop it silently.
func TestBuildAgentToolConfigParams_userLocationKeepsType(t *testing.T) {
	t.Parallel()

	c := toolCfg("web_search")
	c.UserLocation = userLocation(types.StringNull(), types.StringNull(), types.StringNull(), types.StringNull())
	web, diags := webToolOptionsFromModel(context.Background(), c)
	if diags.HasError() {
		t.Fatalf("diags: %+v", diags)
	}
	u, diags := buildAgentToolConfigParams("web_search", param.Opt[bool]{}, "", web)
	if diags.HasError() {
		t.Fatalf("diags: %+v", diags)
	}
	raw, err := json.Marshal(u)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"user_location":{"type":"approximate"}`) {
		t.Fatalf("user_location dropped: %s", raw)
	}
}

// An option set for a tool that does not take it fails the build instead of
// being dropped from the request (apply-time net behind the validator).
func TestBuildAgentToolConfigParams_rejectsOptionOnOtherTool(t *testing.T) {
	t.Parallel()

	tests := []struct {
		tool   string
		web    webToolOptions
		option string
	}{
		{"bash", webToolOptions{allowedDomains: []string{"example.com"}}, "allowed_domains"},
		{"grep", webToolOptions{blockedDomains: []string{"example.com"}}, "blocked_domains"},
		{"web_search", webToolOptions{maxContentTokens: param.NewOpt[int64](10)}, "max_content_tokens"},
		{"web_fetch", webToolOptions{userLocation: &anthropic.BetaManagedAgentsUserLocationParam{Country: param.NewOpt("US")}}, "user_location"},
		{"web_search", webToolOptions{urlSources: &anthropic.BetaManagedAgentsWebFetchURLSourcesParams{}}, "url_sources"},
	}
	for _, tc := range tests {
		t.Run(tc.tool+"/"+tc.option, func(t *testing.T) {
			t.Parallel()
			_, diags := buildAgentToolConfigParams(tc.tool, param.Opt[bool]{}, "", tc.web)
			if !diags.HasError() {
				t.Fatal("expected an error diagnostic")
			}
			if got := diags.Errors()[0].Detail(); !strings.Contains(got, tc.option) || !strings.Contains(got, tc.tool) {
				t.Errorf("detail = %q, want it to name %q and %q", got, tc.option, tc.tool)
			}
		})
	}
}

func TestValidateAgentToolConfigs(t *testing.T) {
	t.Parallel()

	with := func(name string, f func(*agentToolConfigModel)) agentToolConfigModel {
		c := toolCfg(name)
		f(&c)
		return c
	}
	us := userLocation(types.StringValue("US"), types.StringNull(), types.StringNull(), types.StringNull())

	tests := []struct {
		name     string
		config   agentToolConfigModel
		wantPath string // empty: valid
	}{
		{"fetch with all its options", with("web_fetch", func(c *agentToolConfigModel) {
			c.AllowedDomains = strSet("example.com")
			c.MaxContentTokens = types.Int64Value(1000)
		}), ""},
		{"search with all its options", with("web_search", func(c *agentToolConfigModel) {
			c.BlockedDomains = strSet("example.com")
			c.UserLocation = us
		}), ""},
		{"both domain lists", with("web_fetch", func(c *agentToolConfigModel) {
			c.AllowedDomains = strSet("a.example.com")
			c.BlockedDomains = strSet("b.example.com")
		}), "agent_toolset.configs[0].blocked_domains"},
		{"unknown list with a set one", with("web_fetch", func(c *agentToolConfigModel) {
			c.AllowedDomains = types.SetUnknown(types.StringType)
			c.BlockedDomains = strSet("b.example.com")
		}), ""},
		{"max_content_tokens on web_search", with("web_search", func(c *agentToolConfigModel) {
			c.MaxContentTokens = types.Int64Value(1000)
		}), "agent_toolset.configs[0].max_content_tokens"},
		{"user_location on web_fetch", with("web_fetch", func(c *agentToolConfigModel) {
			c.UserLocation = us
		}), "agent_toolset.configs[0].user_location"},
		{"allowed_domains on bash", with("bash", func(c *agentToolConfigModel) {
			c.AllowedDomains = strSet("example.com")
		}), "agent_toolset.configs[0].allowed_domains"},
		{"blocked_domains on read", with("read", func(c *agentToolConfigModel) {
			c.BlockedDomains = strSet("example.com")
		}), "agent_toolset.configs[0].blocked_domains"},
		{"unknown option on bash", with("bash", func(c *agentToolConfigModel) {
			c.MaxContentTokens = types.Int64Unknown()
		}), ""},
		{"options with an unknown name", with("web_fetch", func(c *agentToolConfigModel) {
			c.Name = types.StringUnknown()
			c.UserLocation = us
		}), ""},
		{"empty user_location", with("web_search", func(c *agentToolConfigModel) {
			c.UserLocation = userLocation(types.StringNull(), types.StringNull(), types.StringNull(), types.StringNull())
		}), "agent_toolset.configs[0].user_location"},
		{"user_location with an unknown field", with("web_search", func(c *agentToolConfigModel) {
			c.UserLocation = userLocation(types.StringUnknown(), types.StringNull(), types.StringNull(), types.StringNull())
		}), ""},
		{"fetch with url_sources", with("web_fetch", func(c *agentToolConfigModel) {
			c.URLSources = urlSources(types.StringValue("none"), urlFilter("except", "run_code"), urlFilter("none"))
		}), ""},
		{"url_sources on web_search", with("web_search", func(c *agentToolConfigModel) {
			c.URLSources = urlSources(types.StringValue("none"), noURLFilter, noURLFilter)
		}), "agent_toolset.configs[0].url_sources"},
		{"empty url_sources", with("web_fetch", func(c *agentToolConfigModel) {
			c.URLSources = urlSources(types.StringNull(), noURLFilter, noURLFilter)
		}), "agent_toolset.configs[0].url_sources"},
		{"every url source none", with("web_fetch", func(c *agentToolConfigModel) {
			c.URLSources = urlSources(types.StringValue("none"), urlFilter("none"), urlFilter("none"))
		}), "agent_toolset.configs[0].url_sources"},
		{"every url source none but one unknown", with("web_fetch", func(c *agentToolConfigModel) {
			c.URLSources = urlSources(types.StringUnknown(), urlFilter("none"), urlFilter("none"))
		}), ""},
		{"only without tools", with("web_fetch", func(c *agentToolConfigModel) {
			c.URLSources = urlSources(types.StringNull(), urlFilter("only"), noURLFilter)
		}), "agent_toolset.configs[0].url_sources.client_tool_results.tools"},
		{"tools with all", with("web_fetch", func(c *agentToolConfigModel) {
			c.URLSources = urlSources(types.StringNull(), noURLFilter, urlFilter("all", "web_search"))
		}), "agent_toolset.configs[0].url_sources.server_tool_results.tools"},
		{"unknown filter type with tools", with("web_fetch", func(c *agentToolConfigModel) {
			c.URLSources = urlSources(types.StringNull(), types.ObjectValueMust(agentURLSourceFilterAttrTypes, map[string]attr.Value{
				"type": types.StringUnknown(), "tools": strSet("run_code"),
			}), noURLFilter)
		}), ""},
		{"only with unknown tools", with("web_fetch", func(c *agentToolConfigModel) {
			c.URLSources = urlSources(types.StringNull(), types.ObjectValueMust(agentURLSourceFilterAttrTypes, map[string]attr.Value{
				"type": types.StringValue("only"), "tools": types.SetUnknown(types.StringType),
			}), noURLFilter)
		}), ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			diags := validateAgentToolConfigs(context.Background(), agentToolsetObj(t, toolCfgObj(t, tc.config)))
			if tc.wantPath == "" {
				if diags.HasError() {
					t.Fatalf("unexpected diags: %+v", diags)
				}
				return
			}
			if diags.ErrorsCount() != 1 {
				t.Fatalf("got %d errors, want 1: %+v", diags.ErrorsCount(), diags)
			}
			withPath, ok := diags.Errors()[0].(interface{ Path() path.Path })
			if !ok || withPath.Path().String() != tc.wantPath {
				t.Errorf("error = %+v, want it on %s", diags.Errors()[0], tc.wantPath)
			}
		})
	}

	// An error on one entry must not stop the checks of the next ones.
	t.Run("every entry is checked", func(t *testing.T) {
		t.Parallel()
		diags := validateAgentToolConfigs(context.Background(), agentToolsetObj(t,
			toolCfgObj(t, with("web_search", func(c *agentToolConfigModel) { c.MaxContentTokens = types.Int64Value(1) })),
			toolCfgObj(t, with("bash", func(c *agentToolConfigModel) { c.AllowedDomains = strSet("example.com") })),
		))
		var paths []string
		for _, e := range diags.Errors() {
			if withPath, ok := e.(interface{ Path() path.Path }); ok {
				paths = append(paths, withPath.Path().String())
			}
		}
		want := []string{"agent_toolset.configs[0].max_content_tokens", "agent_toolset.configs[1].allowed_domains"}
		if strings.Join(paths, ",") != strings.Join(want, ",") {
			t.Errorf("error paths = %v, want %v", paths, want)
		}
	})

	t.Run("null, unknown and unknown elements", func(t *testing.T) {
		t.Parallel()
		for _, obj := range []types.Object{
			types.ObjectNull(agentToolsetAttrTypes),
			types.ObjectUnknown(agentToolsetAttrTypes),
			agentToolsetObj(t, types.ObjectUnknown(agentToolConfigAttrTypes)),
		} {
			if diags := validateAgentToolConfigs(context.Background(), obj); diags.HasError() {
				t.Errorf("%v: unexpected diags: %+v", obj, diags)
			}
		}
	})
}

// The resource's config validators reject an option on the wrong tool at plan
// time, read from a configuration built from the resource schema.
func TestAgentResourceConfigValidators_rejectWebOptionOnOtherTool(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	r := &AgentResource{}

	c := toolCfg("web_search")
	c.MaxContentTokens = types.Int64Value(1000)
	toolset, err := agentToolsetObj(t, toolCfgObj(t, c)).ToTerraformValue(ctx)
	if err != nil {
		t.Fatal(err)
	}
	vals := schematest.NullValues(t, r)
	vals["agent_toolset"] = toolset
	vals["model"] = tftypes.NewValue(tftypes.String, "claude-sonnet-4-6")
	vals["name"] = tftypes.NewValue(tftypes.String, "n")
	req := resource.ValidateConfigRequest{Config: tfsdk.Config{
		Schema: schematest.ResourceSchema(t, r),
		Raw:    tftypes.NewValue(schematest.ResourceObjectType(t, r), vals),
	}}

	var resp resource.ValidateConfigResponse
	for _, v := range r.ConfigValidators(ctx) {
		v.ValidateResource(ctx, req, &resp)
	}
	if resp.Diagnostics.ErrorsCount() != 1 {
		t.Fatalf("got %d errors, want 1: %+v", resp.Diagnostics.ErrorsCount(), resp.Diagnostics)
	}
	if got := resp.Diagnostics.Errors()[0].Detail(); !strings.Contains(got, "max_content_tokens") {
		t.Errorf("detail = %q, want it to name max_content_tokens", got)
	}
}

// Read maps the web options verbatim: echoed values (in any order, the lists
// are sets) match the configuration, an unset option the API omits stays null,
// and one set out of band surfaces as drift.
func TestMapAgentToolsetToState_webOptions(t *testing.T) {
	t.Parallel()

	search := toolCfg("web_search")
	search.AllowedDomains = strSet("docs.example.com", "arxiv.org")
	search.UserLocation = userLocation(types.StringValue("US"), types.StringNull(), types.StringNull(), types.StringValue("America/Los_Angeles"))
	fetch := toolCfg("web_fetch")
	current := agentToolsetObj(t, toolCfgObj(t, search), toolCfgObj(t, fetch))

	tests := []struct {
		name      string
		fetchJSON string
		wantFetch agentToolConfigModel
	}{
		{
			name:      "unset stays null",
			fetchJSON: `{"type":"web_fetch","name":"web_fetch","enabled":true,"permission_policy":{"type":"always_ask"},"max_content_tokens":null}`,
			wantFetch: fetch,
		},
		{
			name:      "out-of-band value surfaces",
			fetchJSON: `{"type":"web_fetch","name":"web_fetch","enabled":true,"permission_policy":{"type":"always_ask"},"blocked_domains":["ads.example.com"],"max_content_tokens":2000}`,
			wantFetch: func() agentToolConfigModel {
				c := fetch
				c.BlockedDomains = strSet("ads.example.com")
				c.MaxContentTokens = types.Int64Value(2000)
				return c
			}(),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			agent := parseAgentWithTools(t, `[{"type":"agent_toolset_20260401",
				"default_config":{"enabled":true,"permission_policy":{"type":"always_ask"}},
				"configs":[`+tc.fetchJSON+`,
					{"type":"web_search","name":"web_search","enabled":true,"permission_policy":{"type":"always_ask"},
					 "allowed_domains":["arxiv.org","docs.example.com"],
					 "user_location":{"type":"approximate","country":"US","city":null,"region":null,"timezone":"America/Los_Angeles"}}]}]`)
			data := AgentResourceModel{AgentToolset: current}
			if diags := mapAgentResponseToState(context.Background(), agent, &data, readMapping); diags.HasError() {
				t.Fatalf("diags: %+v", diags)
			}
			got := agentToolConfigsFromToolset(t, data.AgentToolset)
			if len(got) != 2 {
				t.Fatalf("got %d configs, want 2", len(got))
			}
			for i, want := range []agentToolConfigModel{search, tc.wantFetch} {
				gotObj, wantObj := toolCfgObj(t, got[i]), toolCfgObj(t, want)
				if !gotObj.Equal(wantObj) {
					t.Errorf("config %d = %v, want %v", i, gotObj, wantObj)
				}
			}
		})
	}
}

// url_sources is read verbatim: the object form the API answers in maps back
// to the configured value (tools are a set), a null or absent key stays null,
// the string shorthand is read too, and a value set out of band surfaces.
func TestMapAgentToolsetToState_urlSources(t *testing.T) {
	t.Parallel()

	configured := urlSources(types.StringValue("none"), urlFilter("except", "run_code", "shell"), noURLFilter)
	fetch := toolCfg("web_fetch")
	fetch.URLSources = configured
	current := agentToolsetObj(t, toolCfgObj(t, fetch))

	tests := []struct {
		name    string
		sources string
		want    types.Object
	}{
		{
			name: "echo",
			sources: `{"user_input":{"type":"none"},"client_tool_results":{"type":"except","tools":[` +
				`{"type":"tool_reference","name":"shell"},{"type":"tool_reference","name":"run_code"}]},"server_tool_results":null}`,
			want: configured,
		},
		{
			name: "shorthand keys",
			sources: `{"user_input":"none","client_tool_results":{"type":"except","tools":[` +
				`{"type":"tool_reference","name":"run_code"},{"type":"tool_reference","name":"shell"}]},"server_tool_results":"all"}`,
			want: urlSources(types.StringValue("none"), urlFilter("except", "run_code", "shell"), urlFilter("all")),
		},
		{
			name:    "out-of-band filter",
			sources: `{"user_input":{"type":"none"},"client_tool_results":{"type":"all"}}`,
			want:    urlSources(types.StringValue("none"), urlFilter("all"), noURLFilter),
		},
		{
			name:    "removed out of band",
			sources: `null`,
			want:    types.ObjectNull(agentURLSourcesAttrTypes),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			agent := parseAgentWithTools(t, `[{"type":"agent_toolset_20260401",
				"default_config":{"enabled":true,"permission_policy":{"type":"always_allow"}},
				"configs":[{"type":"web_fetch","name":"web_fetch","enabled":true,"permission_policy":{"type":"always_allow"},
					"url_sources":`+tc.sources+`}]}]`)
			data := AgentResourceModel{AgentToolset: current}
			if diags := mapAgentResponseToState(context.Background(), agent, &data, readMapping); diags.HasError() {
				t.Fatalf("diags: %+v", diags)
			}
			got := agentToolConfigsFromToolset(t, data.AgentToolset)
			if len(got) != 1 || !got[0].URLSources.Equal(tc.want) {
				t.Errorf("url_sources = %v, want %v", got[0].URLSources, tc.want)
			}
		})
	}

	// The data sources map it the same way.
	agent := parseAgentWithTools(t, `[{"type":"agent_toolset_20260401",
		"default_config":{"enabled":true,"permission_policy":{"type":"always_allow"}},
		"configs":[{"type":"web_fetch","name":"web_fetch","enabled":true,"permission_policy":{"type":"always_allow"},
			"url_sources":{"user_input":null,"client_tool_results":{"type":"none"},"server_tool_results":{"type":"only","tools":[{"type":"tool_reference","name":"web_search"}]}}}]}]`)
	var ds AgentDataSourceModel
	if diags := mapAgentResponseToDataSource(agent, &ds); diags.HasError() {
		t.Fatalf("diags: %+v", diags)
	}
	got := agentToolConfigsFromToolset(t, ds.AgentToolset)
	want := urlSources(types.StringNull(), urlFilter("none"), urlFilter("only", "web_search"))
	if len(got) != 1 || !got[0].URLSources.Equal(want) {
		t.Errorf("data source url_sources = %v, want %v", got, want)
	}
}

// On import, and for an untracked config, the web options are mapped as the
// API returns them.
func TestMapAgentToolsetToState_webOptionsImport(t *testing.T) {
	t.Parallel()

	agent := parseAgentWithTools(t, `[{"type":"agent_toolset_20260401",
		"default_config":{"enabled":true,"permission_policy":{"type":"always_allow"}},
		"configs":[{"type":"web_search","name":"web_search","enabled":true,"permission_policy":{"type":"always_allow"},
			"blocked_domains":["ads.example.com"],"user_location":{"type":"approximate","city":"Paris"}}]}]`)
	data := AgentResourceModel{AgentToolset: types.ObjectNull(agentToolsetAttrTypes)}
	if diags := mapAgentResponseToState(context.Background(), agent, &data, readMapping); diags.HasError() {
		t.Fatalf("diags: %+v", diags)
	}
	got := agentToolConfigsFromToolset(t, data.AgentToolset)
	want := toolCfg("web_search")
	want.Enabled = types.BoolValue(true)
	want.PermissionPolicy = types.StringValue("always_allow")
	want.BlockedDomains = strSet("ads.example.com")
	want.UserLocation = userLocation(types.StringNull(), types.StringValue("Paris"), types.StringNull(), types.StringNull())
	if len(got) != 1 || !toolCfgObj(t, got[0]).Equal(toolCfgObj(t, want)) {
		t.Errorf("got %+v, want %+v", got, want)
	}
}

// The API may store domain entries normalized and resolve values the
// configuration leaves unset. Create and Update must still store the plan, or
// Terraform fails the apply with "Provider produced inconsistent result after
// apply". Read keeps the prior spelling of a domain the API compares as equal,
// and reports a value the configuration does not have as drift.
func TestAgentResource_webOptionsAsTheAPIStoresThem(t *testing.T) {
	t.Parallel()

	search := toolCfg("web_search")
	search.AllowedDomains = strSet("Docs.Example.com", "arxiv.org/")
	search.UserLocation = userLocation(types.StringValue("US"), types.StringNull(), types.StringNull(), types.StringNull())
	fetch := toolCfg("web_fetch")
	fetch.BlockedDomains = strSet("Ads.Example.com")
	toolset, diags := types.ObjectValue(agentToolsetAttrTypes, map[string]attr.Value{
		"default_enabled":           types.BoolValue(true),
		"default_permission_policy": types.StringValue("always_ask"),
		"configs": types.ListValueMust(types.ObjectType{AttrTypes: agentToolConfigAttrTypes},
			[]attr.Value{toolCfgObj(t, search), toolCfgObj(t, fetch)}),
	})
	if diags.HasError() {
		t.Fatalf("build agent_toolset: %+v", diags)
	}

	agent := parseAgentWithTools(t, `[{"type":"agent_toolset_20260401",
		"default_config":{"enabled":true,"permission_policy":{"type":"always_ask"}},
		"configs":[
			{"type":"web_search","name":"web_search","enabled":true,"permission_policy":{"type":"always_ask"},
			 "allowed_domains":["arxiv.org","docs.example.com"],
			 "user_location":{"type":"approximate","country":"US","timezone":"America/New_York"}},
			{"type":"web_fetch","name":"web_fetch","enabled":true,"permission_policy":{"type":"always_ask"},
			 "blocked_domains":["ads.example.com"],"max_content_tokens":100000}]}]`)
	planned := plannedAgent(agent, toolset, types.ListNull(types.ObjectType{AttrTypes: agentMCPToolsetAttrTypes}))
	created, updated, read := applyAndRead(t, agentServer(t, agent), agent, planned)

	for step, data := range map[string]AgentResourceModel{"create": created, "update": updated} {
		if !data.AgentToolset.Equal(planned.AgentToolset) {
			t.Errorf("%s: agent_toolset = %s\nplanned:        %s", step, data.AgentToolset, planned.AgentToolset)
		}
	}

	got := agentToolConfigsFromToolset(t, read.AgentToolset)
	if len(got) != 2 {
		t.Fatalf("read: got %d configs, want 2", len(got))
	}
	wantSearch := search
	wantSearch.UserLocation = userLocation(types.StringValue("US"), types.StringNull(), types.StringNull(), types.StringValue("America/New_York"))
	wantFetch := fetch
	wantFetch.MaxContentTokens = types.Int64Value(100000)
	for i, want := range []agentToolConfigModel{wantSearch, wantFetch} {
		if gotObj, wantObj := toolCfgObj(t, got[i]), toolCfgObj(t, want); !gotObj.Equal(wantObj) {
			t.Errorf("read: config %d = %v\nwant %v", i, gotObj, wantObj)
		}
	}
}

// A domain set keeps the configured spelling only when the API holds the same
// entries under the API's comparison.
func TestDomainSetToState(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		current     types.Set
		api         types.Set
		wantCurrent bool
	}{
		{"case and trailing slash", strSet("Docs.Example.com", "arxiv.org/"), strSet("arxiv.org", "docs.example.com"), true},
		{"another entry", strSet("docs.example.com"), strSet("api.example.com"), false},
		{"an entry fewer", strSet("a.example.com", "b.example.com"), strSet("a.example.com"), false},
		{"entries current repeats", strSet("example.com", "Example.com"), strSet("example.com", "other.com"), false},
		{"null current", types.SetNull(types.StringType), strSet("example.com"), false},
		{"null api", strSet("example.com"), types.SetNull(types.StringType), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			want := attr.Value(tc.api)
			if tc.wantCurrent {
				want = tc.current
			}
			if got := domainSetToState(tc.current, tc.api); !got.Equal(want) {
				t.Errorf("got %s, want %s", got, want)
			}
		})
	}
}

// Both data sources expose the web options of every built-in config, and null
// for the tools that have none.
func TestMapAgentToDataSources_webOptions(t *testing.T) {
	t.Parallel()

	agent := parseAgentWithTools(t, `[{"type":"agent_toolset_20260401",
		"default_config":{"enabled":true,"permission_policy":{"type":"always_allow"}},
		"configs":[
			{"type":"web_fetch","name":"web_fetch","enabled":true,"permission_policy":{"type":"always_allow"},
			 "allowed_domains":["docs.example.com"],"max_content_tokens":50000},
			{"type":"bash","name":"bash","enabled":false,"permission_policy":{"type":"always_ask"}}]}]`)

	var data AgentDataSourceModel
	if diags := mapAgentResponseToDataSource(agent, &data); diags.HasError() {
		t.Fatalf("diags: %+v", diags)
	}
	got := agentToolConfigsFromToolset(t, data.AgentToolset)
	fetch := toolCfg("web_fetch")
	fetch.Enabled = types.BoolValue(true)
	fetch.PermissionPolicy = types.StringValue("always_allow")
	fetch.AllowedDomains = strSet("docs.example.com")
	fetch.MaxContentTokens = types.Int64Value(50000)
	bash := toolCfg("bash")
	bash.Enabled = types.BoolValue(false)
	bash.PermissionPolicy = types.StringValue("always_ask")
	if len(got) != 2 || !toolCfgObj(t, got[0]).Equal(toolCfgObj(t, fetch)) || !toolCfgObj(t, got[1]).Equal(toolCfgObj(t, bash)) {
		t.Fatalf("got %+v", got)
	}

	obj, diags := mapAgentToDataSourceObject(agent)
	if diags.HasError() {
		t.Fatalf("diags: %+v", diags)
	}
	listToolset, ok := obj.(types.Object).Attributes()["agent_toolset"].(types.Object)
	if !ok || !listToolset.Equal(data.AgentToolset) {
		t.Errorf("agents data source toolset = %v, want %v", listToolset, data.AgentToolset)
	}
}
