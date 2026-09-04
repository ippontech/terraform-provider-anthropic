// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

// Read-only smoke test for the federation rules list. This only asserts that
// rules.# is set (the org's rule set isn't deterministic), since pagination
// and the issuer_id/include_archived query params stay covered
// deterministically by the httptest-based unit tests in package federation.
//
// Gated on acctest.PreCheckOAuth: no test org exists yet for WIF writes and CI
// has no durable org:admin token, so this runs locally only.
func TestAccFederationRulesDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckOAuth(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `data "anthropic_federation_rules" "all" {}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.anthropic_federation_rules.all", "rules.#"),
				),
			},
		},
	})
}
