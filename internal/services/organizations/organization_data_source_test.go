// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package organizations_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

// TestAccOrganizationDataSource reads the organization tied to the admin key.
// GET /v1/organizations/me is read-only and organization-wide, so it is safe to
// run live without a dedicated test organization.
func TestAccOrganizationDataSource(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckAdmin(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccOrganizationDataSourceConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.anthropic_organization.current", "id"),
					resource.TestCheckResourceAttrSet("data.anthropic_organization.current", "name"),
					resource.TestCheckResourceAttr("data.anthropic_organization.current", "type", "organization"),
				),
			},
		},
	})
}

const testAccOrganizationDataSourceConfig = `
data "anthropic_organization" "current" {}
`
