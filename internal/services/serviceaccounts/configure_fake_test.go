// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package serviceaccounts_test

import (
	"context"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/serviceaccounts"
)

// TestServiceAccounts_requireAuthToken: every resource and data source of the
// package refuses to run with API keys alone, which these endpoints reject,
// before any request.
func TestServiceAccounts_requireAuthToken(t *testing.T) {
	t.Parallel()
	for name, block := range map[string]string{
		"anthropic_service_account resource":           `resource "anthropic_service_account" "test" { name = "x" }`,
		"anthropic_service_account_workspace resource": "resource \"anthropic_service_account_workspace\" \"test\" {\n  service_account_id = \"svac_x\"\n  workspace_id = \"wrkspc_x\"\n  workspace_role = \"workspace_user\"\n}",
		"anthropic_service_account":                    `data "anthropic_service_account" "test" { id = "svac_x" }`,
		"anthropic_service_accounts":                   `data "anthropic_service_accounts" "test" {}`,
		"anthropic_service_account_workspaces":         `data "anthropic_service_account_workspaces" "test" { service_account_id = "svac_x" }`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newServiceAccountsFake(t, 0)
			tfresource.UnitTest(t, tfresource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
				Steps: []tfresource.TestStep{{
					Config:      providertest.ProviderConfig(sdktest.APIKey, sdktest.AdminKey) + block,
					ExpectError: regexp.MustCompile(`Missing\s+OAuth\s+Token`),
				}},
			})
			srv.AssertCalled("", 0)
		})
	}
}

// TestServiceAccounts_rejectForeignProviderData covers the Configure guard
// Terraform cannot reach: provider data of another type is an error.
func TestServiceAccounts_rejectForeignProviderData(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for name, r := range map[string]resource.Resource{
		"anthropic_service_account":           serviceaccounts.NewServiceAccountResource(),
		"anthropic_service_account_workspace": serviceaccounts.NewServiceAccountWorkspaceResource(),
	} {
		var resp resource.ConfigureResponse
		r.(resource.ResourceWithConfigure).Configure(ctx, resource.ConfigureRequest{ProviderData: 42}, &resp)
		if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Unexpected Resource Configure Type" {
			t.Errorf("%s: Configure(int) diagnostics = %v, want Unexpected Resource Configure Type", name, resp.Diagnostics)
		}
	}
	for name, d := range map[string]datasource.DataSource{
		"anthropic_service_account":            serviceaccounts.NewServiceAccountDataSource(),
		"anthropic_service_accounts":           serviceaccounts.NewServiceAccountsDataSource(),
		"anthropic_service_account_workspaces": serviceaccounts.NewServiceAccountWorkspacesDataSource(),
	} {
		var resp datasource.ConfigureResponse
		d.(datasource.DataSourceWithConfigure).Configure(ctx, datasource.ConfigureRequest{ProviderData: 42}, &resp)
		if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Unexpected Data Source Configure Type" {
			t.Errorf("%s: Configure(int) diagnostics = %v, want Unexpected Data Source Configure Type", name, resp.Diagnostics)
		}
	}
}
