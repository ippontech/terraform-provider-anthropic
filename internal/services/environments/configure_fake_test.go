// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package environments_test

import (
	"context"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/environments"
)

// TestEnvironments_requireAPIKey: the resource and both data sources refuse
// to run with an Admin key alone, before any request.
func TestEnvironments_requireAPIKey(t *testing.T) {
	t.Parallel()
	for name, block := range map[string]string{
		"anthropic_environment resource": `resource "anthropic_environment" "test" { name = "x" }`,
		"anthropic_environment":          `data "anthropic_environment" "test" { environment_id = "env_x" }`,
		"anthropic_environments":         `data "anthropic_environments" "test" {}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newEnvironmentsFake(t)
			tfresource.UnitTest(t, tfresource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
				Steps: []tfresource.TestStep{{
					Config:      providertest.ProviderConfig(sdktest.AdminKey) + block,
					ExpectError: regexp.MustCompile(`Missing\s+API\s+Key`),
				}},
			})
			srv.AssertCalled("", 0)
		})
	}
}

// TestEnvironments_rejectForeignProviderData covers the Configure guard
// Terraform cannot reach: provider data of another type is an error.
func TestEnvironments_rejectForeignProviderData(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var rresp resource.ConfigureResponse
	environments.NewEnvironmentResource().(resource.ResourceWithConfigure).Configure(ctx, resource.ConfigureRequest{ProviderData: 42}, &rresp)
	if !rresp.Diagnostics.HasError() || rresp.Diagnostics.Errors()[0].Summary() != "Unexpected Resource Configure Type" {
		t.Errorf("anthropic_environment: Configure(int) diagnostics = %v, want Unexpected Resource Configure Type", rresp.Diagnostics)
	}
	for name, d := range map[string]datasource.DataSource{
		"anthropic_environment":  environments.NewEnvironmentDataSource(),
		"anthropic_environments": environments.NewEnvironmentsDataSource(),
	} {
		var resp datasource.ConfigureResponse
		d.(datasource.DataSourceWithConfigure).Configure(ctx, datasource.ConfigureRequest{ProviderData: 42}, &resp)
		if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Unexpected Data Source Configure Type" {
			t.Errorf("%s: Configure(int) diagnostics = %v, want Unexpected Data Source Configure Type", name, resp.Diagnostics)
		}
	}
}
