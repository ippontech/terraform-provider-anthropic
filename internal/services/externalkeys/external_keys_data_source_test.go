// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package externalkeys_test

import (
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

// CMEK must be turned on per organization, and whether the list endpoint
// answers with an empty list or an error for an organization without it is
// not documented. The terraform-tests organization was never checked, so this
// smoke test runs only with an explicit opt-in on top of PreCheckAdmin, and CI
// (which does not set it) skips it. Pagination and mapping stay covered by the
// httptest-based unit tests.
func preCheckCMEKAcc(t *testing.T) {
	t.Helper()
	acctest.PreCheckAdmin(t)
	if os.Getenv("ANTHROPIC_CMEK_ACC") == "" {
		t.Skip("skipping: ANTHROPIC_CMEK_ACC is not set. Set it to read the external keys of an organization that has CMEK turned on.")
	}
}

func TestAccExternalKeysDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheckCMEKAcc(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: `data "anthropic_external_keys" "all" {}`,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.anthropic_external_keys.all", "external_keys.#"),
				),
			},
		},
	})
}
