// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package memorystores_test

import (
	"context"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/memorystores"
)

// TestMemoryStores_requireAPIKey: every resource and data source of the
// package refuses to run without the standard key, before any request.
func TestMemoryStores_requireAPIKey(t *testing.T) {
	t.Parallel()
	for name, block := range map[string]string{
		"anthropic_memory_store resource": "resource \"anthropic_memory_store\" \"test\" {\n  name = \"x\"\n  description = \"x\"\n}",
		"anthropic_memory resource":       "resource \"anthropic_memory\" \"test\" {\n  memory_store_id = \"memstore_x\"\n  path = \"/x\"\n  content = \"x\"\n}",
		"anthropic_memory_store":          `data "anthropic_memory_store" "test" { id = "memstore_x" }`,
		"anthropic_memory_stores":         `data "anthropic_memory_stores" "test" {}`,
		"anthropic_memory":                "data \"anthropic_memory\" \"test\" {\n  memory_store_id = \"memstore_x\"\n  id = \"mem_x\"\n}",
		"anthropic_memories":              `data "anthropic_memories" "test" { memory_store_id = "memstore_x" }`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newMemoryFake(t, 0)
			tfresource.UnitTest(t, tfresource.TestCase{
				ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
				Steps: []tfresource.TestStep{{
					Config:      providertest.ProviderConfig(sdktest.AdminKey, sdktest.AuthToken) + block,
					ExpectError: regexp.MustCompile(`Missing\s+API\s+Key`),
				}},
			})
			srv.AssertCalled("", 0)
		})
	}
}

// TestMemoryStores_rejectForeignProviderData covers the Configure guard
// Terraform cannot reach: provider data of another type is an error.
func TestMemoryStores_rejectForeignProviderData(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	for name, r := range map[string]resource.Resource{
		"anthropic_memory_store": memorystores.NewMemoryStoreResource(),
		"anthropic_memory":       memorystores.NewMemoryResource(),
	} {
		var resp resource.ConfigureResponse
		r.(resource.ResourceWithConfigure).Configure(ctx, resource.ConfigureRequest{ProviderData: 42}, &resp)
		if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Unexpected Resource Configure Type" {
			t.Errorf("%s: Configure(int) diagnostics = %v, want Unexpected Resource Configure Type", name, resp.Diagnostics)
		}
	}
	for name, d := range map[string]datasource.DataSource{
		"anthropic_memory_store":  memorystores.NewMemoryStoreDataSource(),
		"anthropic_memory_stores": memorystores.NewMemoryStoresDataSource(),
		"anthropic_memory":        memorystores.NewMemoryDataSource(),
		"anthropic_memories":      memorystores.NewMemoriesDataSource(),
	} {
		var resp datasource.ConfigureResponse
		d.(datasource.DataSourceWithConfigure).Configure(ctx, datasource.ConfigureRequest{ProviderData: 42}, &resp)
		if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Unexpected Data Source Configure Type" {
			t.Errorf("%s: Configure(int) diagnostics = %v, want Unexpected Data Source Configure Type", name, resp.Diagnostics)
		}
	}
}
