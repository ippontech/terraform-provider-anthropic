// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package userprofiles_test

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

// preCheckUserProfilesAcc is defined once, in user_profile_resource_test.go,
// and shared by every acceptance test in this package (resource and both
// data sources).

const testAccUserProfilesDataSourceConfig = `
data "anthropic_user_profiles" "test" {}
`

func TestAccUserProfilesDataSource_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheckUserProfilesAcc(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccUserProfilesDataSourceConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.anthropic_user_profiles.test", "user_profiles.#"),
				),
			},
		},
	})
}

func testAccUserProfileDataSourceConfig() string {
	return `
data "anthropic_user_profiles" "all" {
  lifecycle {
    postcondition {
      condition     = length(self.user_profiles) > 0
      error_message = "This test needs at least one user profile in the organization to look up by id; create one first."
    }
  }
}

data "anthropic_user_profile" "test" {
  id = data.anthropic_user_profiles.all.user_profiles[0].id
}
`
}

func TestAccUserProfileDataSource_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheckUserProfilesAcc(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccUserProfileDataSourceConfig(),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("data.anthropic_user_profile.test", "id"),
					resource.TestCheckResourceAttrSet("data.anthropic_user_profile.test", "type"),
				),
			},
		},
	})
}
