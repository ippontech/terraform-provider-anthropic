// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package userprofiles_test

import (
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

// User profiles are a standard-API (non-admin) resource, so in principle they
// support full create/read/update/delete acceptance tests like vaults and
// memory stores. In practice, GET/POST /v1/user_profiles returned a plain 404
// for the terraform-tests workspace's standard key when probed on 2026-09-30
// (all three beta header variants, with and without ?beta=true): the beta is
// not enabled for this organization. There is no way to run these live, so
// they are gated behind an explicit opt-in on top of the usual PreCheck, and
// CI (which does not set it) always skips them.
func preCheckUserProfilesAcc(t *testing.T) {
	acctest.PreCheck(t)
	if os.Getenv("ANTHROPIC_USER_PROFILES_ACC") == "" {
		t.Skip("skipping: ANTHROPIC_USER_PROFILES_ACC is not set. The user profiles beta returns 404 for " +
			"the terraform-tests organization as of 2026-09-30; set this variable once it is enabled to run " +
			"these tests live.")
	}
}

const testAccUserProfileResourceBasicConfig = `
resource "anthropic_user_profile" "test" {
  access_type = "application"
  external_id = "tf-acc-test-user-1"
  name        = "TF Acceptance Test User"
}
`

func TestAccUserProfileResource_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheckUserProfilesAcc(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccUserProfileResourceBasicConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_user_profile.test", "id"),
					resource.TestCheckResourceAttr("anthropic_user_profile.test", "access_type", "application"),
					resource.TestCheckResourceAttr("anthropic_user_profile.test", "external_id", "tf-acc-test-user-1"),
					resource.TestCheckResourceAttr("anthropic_user_profile.test", "name", "TF Acceptance Test User"),
					resource.TestCheckResourceAttr("anthropic_user_profile.test", "type", "user_profile"),
					resource.TestCheckResourceAttrSet("anthropic_user_profile.test", "created_at"),
					resource.TestCheckResourceAttrSet("anthropic_user_profile.test", "updated_at"),
				),
			},
			{
				ResourceName:      "anthropic_user_profile.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

const testAccUserProfileResourceUpdateConfigV1 = `
resource "anthropic_user_profile" "test" {
  access_type = "application"
  name        = "tf-acc-test-user-update-v1"

  metadata = {
    team = "terraform"
    env  = "test"
  }
}
`

// V2 renames the profile, switches access_type, changes one metadata value,
// and removes another key, exercising buildUserProfileMetadataUpdate's
// empty-string removal path.
const testAccUserProfileResourceUpdateConfigV2 = `
resource "anthropic_user_profile" "test" {
  access_type = "passthrough"
  name        = "tf-acc-test-user-update-v2"

  metadata = {
    team = "platform"
  }
}
`

func TestAccUserProfileResource_update(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheckUserProfilesAcc(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccUserProfileResourceUpdateConfigV1,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("anthropic_user_profile.test", "access_type", "application"),
					resource.TestCheckResourceAttr("anthropic_user_profile.test", "name", "tf-acc-test-user-update-v1"),
					resource.TestCheckResourceAttr("anthropic_user_profile.test", "metadata.team", "terraform"),
					resource.TestCheckResourceAttr("anthropic_user_profile.test", "metadata.env", "test"),
				),
			},
			{
				Config: testAccUserProfileResourceUpdateConfigV2,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("anthropic_user_profile.test", "access_type", "passthrough"),
					resource.TestCheckResourceAttr("anthropic_user_profile.test", "name", "tf-acc-test-user-update-v2"),
					resource.TestCheckResourceAttr("anthropic_user_profile.test", "metadata.team", "platform"),
					// env was removed in config and must be cleared server-side.
					resource.TestCheckNoResourceAttr("anthropic_user_profile.test", "metadata.env"),
				),
			},
		},
	})
}
