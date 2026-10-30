// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package claudetag_test

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
	"github.com/ippontech/terraform-provider-anthropic/internal/services/claudetag"
)

// TestClaudeTag_requireAdminKey: every resource and data source of the
// package refuses to run with the standard key alone, before any request:
// the Claude Tag API takes the organization's scoped Admin API key (#scopes).
func TestClaudeTag_requireAdminKey(t *testing.T) {
	t.Parallel()
	for name, block := range map[string]string{
		"slack_channel resource":            "resource \"anthropic_claude_tag_slack_channel\" \"test\" {\n  slack = { workspace_id = \"T01\", channel_id = \"C01\" }\n}\n",
		"slack_workspace_settings resource": "resource \"anthropic_claude_tag_slack_workspace_settings\" \"test\" {\n  slack = { workspace_id = \"T01\" }\n}\n",
		"bundle_target resource":            "resource \"anthropic_claude_tag_bundle_target\" \"test\" {\n  bundle_id = \"capp_01\"\n  slack_workspace_id = \"slws_01\"\n}\n",
		"bundle":                            `data "anthropic_claude_tag_bundle" "test" { id = "capp_01" }`,
		"bundles":                           `data "anthropic_claude_tag_bundles" "test" {}`,
		"slack_channel":                     `data "anthropic_claude_tag_slack_channel" "test" { id = "slch_01" }`,
		"slack_workspaces":                  `data "anthropic_claude_tag_slack_workspaces" "test" {}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			f := newClaudeTagFake(t, 0)
			resource.UnitTest(t, resource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(f.Server),
				Steps: []resource.TestStep{{
					Config:      providertest.ProviderConfig(sdktest.APIKey) + block,
					ExpectError: regexp.MustCompile(`Missing\s+Admin\s+API\s+Key`),
				}},
			})
			f.AssertCalled("", 0)
		})
	}
}

// TestClaudeTag_configureGuards covers the Configure branches Terraform does
// not reach through a configured provider: provider data of another type,
// none yet, and none with an Admin client.
func TestClaudeTag_configureGuards(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	cases := []struct {
		data any
		want string
	}{{42, "Unexpected %s Configure Type"}, {nil, ""}, {&providerdata.ProviderData{}, "Missing Admin API Key"}}
	for _, r := range []fwresource.Resource{
		claudetag.NewSlackChannelResource(), claudetag.NewSlackWorkspaceSettingsResource(), claudetag.NewBundleTargetResource(),
	} {
		for _, tc := range cases {
			var resp fwresource.ConfigureResponse
			r.(fwresource.ResourceWithConfigure).Configure(ctx, fwresource.ConfigureRequest{ProviderData: tc.data}, &resp)
			if got, want := summary(resp.Diagnostics), strings.ReplaceAll(tc.want, "%s", "Resource"); got != want {
				t.Errorf("%T: Configure(%T) error = %q, want %q", r, tc.data, got, want)
			}
		}
	}
	for _, d := range []datasource.DataSource{
		claudetag.NewBundleDataSource(), claudetag.NewBundlesDataSource(),
		claudetag.NewSlackChannelDataSource(), claudetag.NewSlackWorkspacesDataSource(),
	} {
		for _, tc := range cases {
			var resp datasource.ConfigureResponse
			d.(datasource.DataSourceWithConfigure).Configure(ctx, datasource.ConfigureRequest{ProviderData: tc.data}, &resp)
			if got, want := summary(resp.Diagnostics), strings.ReplaceAll(tc.want, "%s", "Data Source"); got != want {
				t.Errorf("%T: Configure(%T) error = %q, want %q", d, tc.data, got, want)
			}
		}
	}
}

// TestBundleTargetResource_updateIsUnreachable: every argument forces a
// replacement, so Update only reports a provider bug.
func TestBundleTargetResource_updateIsUnreachable(t *testing.T) {
	t.Parallel()
	var resp fwresource.UpdateResponse
	claudetag.NewBundleTargetResource().Update(context.Background(), fwresource.UpdateRequest{}, &resp)
	if got := summary(resp.Diagnostics); got != "Unexpected Update" {
		t.Errorf("Update error = %q", got)
	}
}

func summary(diags diag.Diagnostics) string {
	var out []string
	for _, d := range diags.Errors() {
		out = append(out, d.Summary())
	}
	return strings.Join(out, "; ")
}
