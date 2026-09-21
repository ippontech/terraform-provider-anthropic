// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package deployments_test

import (
	"testing"

	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// The terraform-tests workspace holds no deployment yet (the anthropic_deployment
// resource is #140), so this is a smoke test: the list call must succeed and
// yield a well-formed, possibly empty, list.
func TestAccDeploymentRunsDataSource_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccDeploymentRunsDataSourceBasicConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.anthropic_deployment_runs.test", "runs.#"),
				),
			},
		},
	})
}

const testAccDeploymentRunsDataSourceBasicConfig = `
data "anthropic_deployment_runs" "test" {}
`

func TestAccDeploymentRunsDataSource_unknownDeploymentIsEmpty(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccDeploymentRunsDataSourceUnknownDeploymentConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.anthropic_deployment_runs.test", "runs.#", "0"),
				),
			},
		},
	})
}

const testAccDeploymentRunsDataSourceUnknownDeploymentConfig = `
data "anthropic_deployment_runs" "test" {
  deployment_id = "depl_01AAAAAAAAAAAAAAAAAAAAAA"
  has_error     = true
  trigger_type  = "schedule"
}
`
