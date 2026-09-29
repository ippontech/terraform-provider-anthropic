// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package userprofiles_test

import (
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

// userProfilesAccOptInEnvVar gates the live smoke tests below. The
// user_profiles beta is not enabled for the terraform-tests organization
// (verified 2026-09-30: GET /v1/user_profiles returns 404 for the standard
// key, 401 for the admin key), so acctest.PreCheck alone would fail these
// tests in CI. Set this to "1" only against an organization known to have
// the beta enabled.
const userProfilesAccOptInEnvVar = "ANTHROPIC_USER_PROFILES_ACC"

func preCheckUserProfilesAcc(t *testing.T) {
	acctest.PreCheck(t)
	if os.Getenv(userProfilesAccOptInEnvVar) != "1" {
		t.Skipf("skipping: the user_profiles beta is not enabled for the terraform-tests organization (404 as of 2026-09-30); "+
			"set %s=1 to run this against an organization with the beta enabled", userProfilesAccOptInEnvVar)
	}
}

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
