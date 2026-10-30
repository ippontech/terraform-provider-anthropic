// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package messages_test

import (
	"context"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/messages"
)

// TestMessages_requireAPIKey: the resource and the data source refuse to
// run with an Admin key alone, before any request.
func TestMessages_requireAPIKey(t *testing.T) {
	t.Parallel()
	for name, block := range map[string]string{
		"anthropic_message": `resource "anthropic_message" "test" {
  model      = "claude-opus-5-5"
  max_tokens = 16
  messages   = [{ role = "user", content = "hi" }]
}`,
		"anthropic_count_tokens": `data "anthropic_count_tokens" "test" {
  model    = "claude-opus-5-5"
  messages = [{ role = "user", content = "hi" }]
}`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			srv := newMessagesFake(t)
			tfresource.UnitTest(t, tfresource.TestCase{
				ProtoV6ProviderFactories: factories(srv),
				Steps: []tfresource.TestStep{{
					Config:      providertest.ProviderConfig(sdktest.AdminKey) + block,
					ExpectError: regexp.MustCompile(`(?s)Missing\s+API\s+Key.*requires\s+an\s+API\s+key`),
				}},
			})
			srv.AssertCalled("", 0)
		})
	}
}

// TestMessages_rejectForeignProviderData covers the Configure guard
// Terraform cannot reach: provider data of another type is an error.
func TestMessages_rejectForeignProviderData(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var rresp resource.ConfigureResponse
	messages.NewMessageResource().(resource.ResourceWithConfigure).Configure(ctx, resource.ConfigureRequest{ProviderData: 42}, &rresp)
	if !rresp.Diagnostics.HasError() || rresp.Diagnostics.Errors()[0].Summary() != "Unexpected Resource Configure Type" {
		t.Errorf("anthropic_message: Configure(int) diagnostics = %v, want Unexpected Resource Configure Type", rresp.Diagnostics)
	}
	var dresp datasource.ConfigureResponse
	messages.NewCountTokensDataSource().(datasource.DataSourceWithConfigure).Configure(ctx, datasource.ConfigureRequest{ProviderData: 42}, &dresp)
	if !dresp.Diagnostics.HasError() || dresp.Diagnostics.Errors()[0].Summary() != "Unexpected Data Source Configure Type" {
		t.Errorf("anthropic_count_tokens: Configure(int) diagnostics = %v, want Unexpected Data Source Configure Type", dresp.Diagnostics)
	}
}
