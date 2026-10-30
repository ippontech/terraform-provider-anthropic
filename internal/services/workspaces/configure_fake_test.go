// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package workspaces_test

import (
	"context"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/workspaces"
)

// TestWorkspaces_requireAdminKey: every resource and data source of the
// package refuses to run with the standard key alone, before any request.
func TestWorkspaces_requireAdminKey(t *testing.T) {
	t.Parallel()
	for name, block := range map[string]string{
		"anthropic_workspace resource":            `resource "anthropic_workspace" "test" { name = "x" }`,
		"anthropic_workspace_member resource":     "resource \"anthropic_workspace_member\" \"test\" {\n  workspace_id = \"w\"\n  user_id = \"u\"\n  workspace_role = \"workspace_user\"\n}",
		"anthropic_workspace_rate_limit resource": "resource \"anthropic_workspace_rate_limit\" \"test\" {\n  workspace_id = \"w\"\n  rate_limit_id = \"r\"\n  limits = { requests_per_minute = 1 }\n}",
		"anthropic_workspace":                     `data "anthropic_workspace" "test" { id = "w" }`,
		"anthropic_workspaces":                    `data "anthropic_workspaces" "test" {}`,
		"anthropic_workspace_member":              "data \"anthropic_workspace_member\" \"test\" {\n  workspace_id = \"w\"\n  user_id = \"u\"\n}",
		"anthropic_workspace_members":             `data "anthropic_workspace_members" "test" { workspace_id = "w" }`,
		"anthropic_workspace_rate_limits":         `data "anthropic_workspace_rate_limits" "test" { workspace_id = "w" }`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newWSFake(t, 0)
			tfresource.UnitTest(t, tfresource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
				Steps: []tfresource.TestStep{{
					Config:      providertest.ProviderConfig(sdktest.APIKey) + block,
					ExpectError: regexp.MustCompile(`Missing\s+Admin\s+API\s+Key`),
				}},
			})
			srv.AssertCalled("", 0)
		})
	}
}

// TestWorkspaces_rejectForeignProviderData covers the Configure guard
// Terraform cannot reach: provider data of another type is an error.
func TestWorkspaces_rejectForeignProviderData(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for name, r := range map[string]resource.Resource{
		"anthropic_workspace":            workspaces.NewWorkspaceResource(),
		"anthropic_workspace_member":     workspaces.NewWorkspaceMemberResource(),
		"anthropic_workspace_rate_limit": workspaces.NewWorkspaceRateLimitResource(),
	} {
		var resp resource.ConfigureResponse
		r.(resource.ResourceWithConfigure).Configure(ctx, resource.ConfigureRequest{ProviderData: 42}, &resp)
		if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Unexpected Resource Configure Type" {
			t.Errorf("%s: Configure(int) diagnostics = %v, want Unexpected Resource Configure Type", name, resp.Diagnostics)
		}
	}
	for name, d := range map[string]datasource.DataSource{
		"anthropic_workspace":             workspaces.NewWorkspaceDataSource(),
		"anthropic_workspaces":            workspaces.NewWorkspacesDataSource(),
		"anthropic_workspace_member":      workspaces.NewWorkspaceMemberDataSource(),
		"anthropic_workspace_members":     workspaces.NewWorkspaceMembersDataSource(),
		"anthropic_workspace_rate_limits": workspaces.NewWorkspaceRateLimitsDataSource(),
	} {
		var resp datasource.ConfigureResponse
		d.(datasource.DataSourceWithConfigure).Configure(ctx, datasource.ConfigureRequest{ProviderData: 42}, &resp)
		if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Unexpected Data Source Configure Type" {
			t.Errorf("%s: Configure(int) diagnostics = %v, want Unexpected Data Source Configure Type", name, resp.Diagnostics)
		}
	}
}
