// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package plugins_test

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	fwresource "github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/ippontech/terraform-provider-anthropic/internal/providerdata"
	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/plugins"
)

// TestPlugins_requireAdminKey: every resource and data source of the
// package refuses to run with the standard key alone, before any request.
func TestPlugins_requireAdminKey(t *testing.T) {
	t.Parallel()
	for name, block := range map[string]string{
		"anthropic_plugin resource":                      "resource \"anthropic_plugin\" \"test\" {\n  source_dir = \"d\"\n}\n",
		"anthropic_plugin_version resource":              "resource \"anthropic_plugin_version\" \"test\" {\n  plugin_id = \"plugin_01\"\n  source_dir = \"d\"\n}\n",
		"anthropic_plugin_installation_setting resource": "resource \"anthropic_plugin_installation_setting\" \"test\" {\n  plugin_id = \"plugin_01\"\n  target = \"organization\"\n  installation_preference = \"required\"\n}\n",
		"anthropic_plugin_marketplace_settings resource": "resource \"anthropic_plugin_marketplace_settings\" \"test\" {\n  marketplace_id = \"marketplace_01\"\n  default_installation_preference = \"required\"\n}\n",
		"anthropic_plugin":                               `data "anthropic_plugin" "test" { id = "plugin_01" }`,
		"anthropic_plugins":                              `data "anthropic_plugins" "test" {}`,
		"anthropic_plugin_versions":                      `data "anthropic_plugin_versions" "test" { plugin_id = "plugin_01" }`,
		"anthropic_plugin_marketplace":                   `data "anthropic_plugin_marketplace" "test" { id = "marketplace_01" }`,
		"anthropic_plugin_marketplaces":                  `data "anthropic_plugin_marketplaces" "test" {}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newPluginsFake(t, 0)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
				Steps: []resource.TestStep{{
					Config:      providertest.ProviderConfig(sdktest.APIKey) + block,
					ExpectError: regexp.MustCompile(`Missing\s+Admin\s+API\s+Key`),
				}},
			})
			srv.AssertCalled("", 0)
		})
	}
}

// TestPlugins_configureGuards covers the Configure branches Terraform does
// not reach through a configured provider: provider data of another type is
// an error, no provider data yet (validation before the provider is
// configured) is none, and provider data without an Admin client is the
// missing-key error.
func TestPlugins_configureGuards(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for name, r := range map[string]fwresource.Resource{
		"anthropic_plugin":                      plugins.NewPluginResource(),
		"anthropic_plugin_version":              plugins.NewPluginVersionResource(),
		"anthropic_plugin_installation_setting": plugins.NewPluginInstallationSettingResource(),
		"anthropic_plugin_marketplace_settings": plugins.NewPluginMarketplaceSettingsResource(),
	} {
		c := r.(fwresource.ResourceWithConfigure)
		for _, tc := range []struct {
			data any
			want string
		}{{42, "Unexpected Resource Configure Type"}, {nil, ""}, {&providerdata.ProviderData{}, "Missing Admin API Key"}} {
			var resp fwresource.ConfigureResponse
			c.Configure(ctx, fwresource.ConfigureRequest{ProviderData: tc.data}, &resp)
			if got := summary(resp.Diagnostics); got != tc.want {
				t.Errorf("%s: Configure(%T) error = %q, want %q", name, tc.data, got, tc.want)
			}
		}
	}
	for name, d := range map[string]datasource.DataSource{
		"anthropic_plugin":              plugins.NewPluginDataSource(),
		"anthropic_plugins":             plugins.NewPluginsDataSource(),
		"anthropic_plugin_versions":     plugins.NewPluginVersionsDataSource(),
		"anthropic_plugin_marketplace":  plugins.NewPluginMarketplaceDataSource(),
		"anthropic_plugin_marketplaces": plugins.NewPluginMarketplacesDataSource(),
	} {
		c := d.(datasource.DataSourceWithConfigure)
		for _, tc := range []struct {
			data any
			want string
		}{{42, "Unexpected Data Source Configure Type"}, {nil, ""}, {&providerdata.ProviderData{}, "Missing Admin API Key"}} {
			var resp datasource.ConfigureResponse
			c.Configure(ctx, datasource.ConfigureRequest{ProviderData: tc.data}, &resp)
			if got := summary(resp.Diagnostics); got != tc.want {
				t.Errorf("%s: Configure(%T) error = %q, want %q", name, tc.data, got, tc.want)
			}
		}
	}
}

// summary joins the summaries of the error diagnostics, "" when there is
// none.
func summary(diags diag.Diagnostics) string {
	var out []string
	for _, d := range diags.Errors() {
		out = append(out, d.Summary())
	}
	return strings.Join(out, "; ")
}
