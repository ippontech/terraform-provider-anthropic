// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package agents_test

import (
	"context"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/agents"
)

// TestAgents_requireAPIKey: every resource and data source of the package
// refuses to run with an Admin key alone, before any request.
func TestAgents_requireAPIKey(t *testing.T) {
	t.Parallel()
	for name, block := range map[string]string{
		"anthropic_agent resource": "resource \"anthropic_agent\" \"test\" {\n  name  = \"a\"\n  model = \"claude-sonnet-4-6\"\n}",
		"anthropic_agent":          `data "anthropic_agent" "test" { agent_id = "agent_x" }`,
		"anthropic_agents":         `data "anthropic_agents" "test" {}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newAgentsFake(t)
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

// TestAgents_rejectForeignProviderData covers the Configure guard Terraform
// cannot reach: provider data of another type is an error.
func TestAgents_rejectForeignProviderData(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var resp resource.ConfigureResponse
	agents.NewAgentResource().(resource.ResourceWithConfigure).Configure(ctx, resource.ConfigureRequest{ProviderData: 42}, &resp)
	if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Unexpected Resource Configure Type" {
		t.Errorf("anthropic_agent: Configure(int) diagnostics = %v, want Unexpected Resource Configure Type", resp.Diagnostics)
	}
	for name, d := range map[string]datasource.DataSource{
		"anthropic_agent":  agents.NewAgentDataSource(),
		"anthropic_agents": agents.NewAgentsDataSource(),
	} {
		var resp datasource.ConfigureResponse
		d.(datasource.DataSourceWithConfigure).Configure(ctx, datasource.ConfigureRequest{ProviderData: 42}, &resp)
		if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Unexpected Data Source Configure Type" {
			t.Errorf("%s: Configure(int) diagnostics = %v, want Unexpected Data Source Configure Type", name, resp.Diagnostics)
		}
	}
}
