// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package spendlimits_test

import (
	"context"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	tfresource "github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/ippontech/terraform-provider-anthropic/internal/providertest"
	"github.com/ippontech/terraform-provider-anthropic/internal/sdktest"
	"github.com/ippontech/terraform-provider-anthropic/internal/services/spendlimits"
)

// TestSpendLimits_requireAdminKey: the resource refuses to run with the
// standard key alone, before any request.
func TestSpendLimits_requireAdminKey(t *testing.T) {
	t.Parallel()
	srv := newLimitsFake(t)
	tfresource.UnitTest(t, tfresource.TestCase{
		ProtoV6ProviderFactories: providertest.ProviderFactories(srv.Server),
		Steps: []tfresource.TestStep{{
			Config:      providertest.ProviderConfig(sdktest.APIKey) + "resource \"anthropic_spend_limit\" \"test\" {\n  scope = { type = \"user\", user_id = \"u\" }\n  amount = \"1\"\n}",
			ExpectError: regexp.MustCompile(`Missing\s+Admin\s+API\s+Key`),
		}},
	})
	srv.AssertCalled("", 0)
}

// TestSpendLimits_rejectForeignProviderData covers the Configure guard
// Terraform cannot reach: provider data of another type is an error.
func TestSpendLimits_rejectForeignProviderData(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	var resp resource.ConfigureResponse
	spendlimits.NewSpendLimitResource().(resource.ResourceWithConfigure).Configure(ctx, resource.ConfigureRequest{ProviderData: 42}, &resp)
	if !resp.Diagnostics.HasError() || resp.Diagnostics.Errors()[0].Summary() != "Unexpected Resource Configure Type" {
		t.Errorf("Configure(int) diagnostics = %v, want Unexpected Resource Configure Type", resp.Diagnostics)
	}
}
