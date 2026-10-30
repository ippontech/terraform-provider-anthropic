// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package apikeys_test

import (
	"context"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/apikeys"
)

// TestAPIKeys_requireAdminKey: the resource and both data sources refuse to
// run with the standard key alone, before any request.
func TestAPIKeys_requireAdminKey(t *testing.T) {
	t.Parallel()
	for name, block := range map[string]string{
		"anthropic_api_key resource": `resource "anthropic_api_key" "test" { name = "x" }`,
		"anthropic_api_key":          `data "anthropic_api_key" "test" { id = "apikey_x" }`,
		"anthropic_api_keys":         `data "anthropic_api_keys" "test" {}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newKeysFake(t, 0)
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

// TestAPIKeys_rejectForeignProviderData covers the Configure guard Terraform
// cannot reach: provider data of another type is an error.
func TestAPIKeys_rejectForeignProviderData(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var rresp resource.ConfigureResponse
	apikeys.NewAPIKeyResource().(resource.ResourceWithConfigure).Configure(ctx, resource.ConfigureRequest{ProviderData: 42}, &rresp)
	if !rresp.Diagnostics.HasError() || rresp.Diagnostics.Errors()[0].Summary() != "Unexpected Resource Configure Type" {
		t.Errorf("anthropic_api_key: Configure(int) diagnostics = %v, want Unexpected Resource Configure Type", rresp.Diagnostics)
	}
	for name, d := range map[string]datasource.DataSource{
		"anthropic_api_key":  apikeys.NewAPIKeyDataSource(),
		"anthropic_api_keys": apikeys.NewAPIKeysDataSource(),
	} {
		var resp datasource.ConfigureResponse
		d.(datasource.DataSourceWithConfigure).Configure(ctx, datasource.ConfigureRequest{ProviderData: 42}, &resp)
		if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Unexpected Data Source Configure Type" {
			t.Errorf("%s: Configure(int) diagnostics = %v, want Unexpected Data Source Configure Type", name, resp.Diagnostics)
		}
	}
}
