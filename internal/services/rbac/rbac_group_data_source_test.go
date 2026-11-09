// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package rbac_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

const testAccRBACGroupDataSourceConfig = `
resource "anthropic_rbac_group" "test" {
  name = "tf-acc-rbac-group-data-source"
}

data "anthropic_rbac_group" "test" {
  id = anthropic_rbac_group.test.id
}
`

func TestAccRBACGroupDataSource_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheckEnterpriseAcc(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckRBACGroupDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccRBACGroupDataSourceConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.anthropic_rbac_group.test", "name", "tf-acc-rbac-group-data-source"),
					resource.TestCheckResourceAttr("data.anthropic_rbac_group.test", "source_type", "direct"),
				),
			},
		},
	})
}
