// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package deployments_test

import (
	"context"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/deployments"
)

// TestDeployments_requireAPIKey: the resource and the data source refuse to
// run with an Admin key alone, before any request.
func TestDeployments_requireAPIKey(t *testing.T) {
	t.Parallel()
	for name, block := range map[string]string{
		"anthropic_deployment resource": "resource \"anthropic_deployment\" \"test\" {\n  name = \"x\"\n  agent_id = \"agent_1\"\n  environment_id = \"env_1\"\n  initial_events = \"[]\"\n}",
		"anthropic_deployment_runs":     `data "anthropic_deployment_runs" "test" {}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newDeploymentsFake(t, 0)
			tfresource.UnitTest(t, tfresource.TestCase{
				ProtoV6ProviderFactories: factories(srv),
				Steps: []tfresource.TestStep{{
					Config:      providertest.ProviderConfig(sdktest.AdminKey) + block,
					ExpectError: regexp.MustCompile(`Missing\s+API\s+Key`),
				}},
			})
			srv.AssertCalled("", 0)
		})
	}
}

// TestDeployments_rejectForeignProviderData covers the Configure guard
// Terraform cannot reach: provider data of another type is an error.
func TestDeployments_rejectForeignProviderData(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var rresp resource.ConfigureResponse
	deployments.NewDeploymentResource().(resource.ResourceWithConfigure).Configure(ctx, resource.ConfigureRequest{ProviderData: 42}, &rresp)
	if !rresp.Diagnostics.HasError() || rresp.Diagnostics.Errors()[0].Summary() != "Unexpected Resource Configure Type" {
		t.Errorf("anthropic_deployment: Configure(int) diagnostics = %v, want Unexpected Resource Configure Type", rresp.Diagnostics)
	}
	var dresp datasource.ConfigureResponse
	deployments.NewDeploymentRunsDataSource().(datasource.DataSourceWithConfigure).Configure(ctx, datasource.ConfigureRequest{ProviderData: 42}, &dresp)
	if !dresp.Diagnostics.HasError() || dresp.Diagnostics.Errors()[0].Summary() != "Unexpected Data Source Configure Type" {
		t.Errorf("anthropic_deployment_runs: Configure(int) diagnostics = %v, want Unexpected Data Source Configure Type", dresp.Diagnostics)
	}
}
