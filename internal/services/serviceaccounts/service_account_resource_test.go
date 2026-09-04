// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package serviceaccounts_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// Service accounts require an org:admin OAuth bearer token (ANTHROPIC_AUTH_TOKEN),
// which endpoints in this series reject in favor of API keys. No test org exists
// yet for these writes (same blocker as #58) and CI has no durable org:admin
// token, so these tests are gated on acctest.PreCheckOAuth and run locally only.
//
// There is no hard-delete endpoint for service accounts, so CheckDestroy asserts
// that the service account was archived rather than that it is gone: it remains
// permanently in the organization's (archived) service account list.

func newTestOAuthClient() *anthropic.Client {
	c := anthropic.NewClient(option.WithAuthToken(os.Getenv("ANTHROPIC_AUTH_TOKEN")))
	return &c
}

func testAccCheckServiceAccountArchived(s *terraform.State) error {
	client := newTestOAuthClient()
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "anthropic_service_account" {
			continue
		}
		sa, err := client.Beta.Organization.ServiceAccounts.Get(context.Background(), rs.Primary.ID, anthropic.BetaOrganizationServiceAccountGetParams{})
		if err != nil {
			return fmt.Errorf("service account %s not found after destroy: %w", rs.Primary.ID, err)
		}
		if sa.ArchivedAt.IsZero() {
			return fmt.Errorf("service account %s was not archived on destroy", rs.Primary.ID)
		}
	}
	return nil
}

const testAccServiceAccountResourceBasicConfig = `
resource "anthropic_service_account" "test" {
  name = "tf-acc-test-service-account-basic"
}
`

func TestAccServiceAccountResource_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckOAuth(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckServiceAccountArchived,
		Steps: []resource.TestStep{
			// Create and Read
			{
				Config: testAccServiceAccountResourceBasicConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_service_account.test", "id"),
					resource.TestCheckResourceAttr("anthropic_service_account.test", "name", "tf-acc-test-service-account-basic"),
					resource.TestCheckResourceAttr("anthropic_service_account.test", "organization_role", "developer"),
					resource.TestCheckResourceAttrSet("anthropic_service_account.test", "created_at"),
					resource.TestCheckResourceAttrSet("anthropic_service_account.test", "updated_at"),
					resource.TestCheckNoResourceAttr("anthropic_service_account.test", "archived_at"),
				),
			},
			// ImportState
			{
				ResourceName:      "anthropic_service_account.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

const testAccServiceAccountResourceUpdateConfigV1 = `
resource "anthropic_service_account" "test" {
  name        = "tf-acc-test-service-account-update"
  description = "initial description"
}
`

const testAccServiceAccountResourceUpdateConfigV2 = `
resource "anthropic_service_account" "test" {
  name = "tf-acc-test-service-account-update"
}
`

func TestAccServiceAccountResource_update(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckOAuth(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckServiceAccountArchived,
		Steps: []resource.TestStep{
			{
				Config: testAccServiceAccountResourceUpdateConfigV1,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("anthropic_service_account.test", "name", "tf-acc-test-service-account-update"),
					resource.TestCheckResourceAttr("anthropic_service_account.test", "description", "initial description"),
				),
			},
			// description removed from config must clear it server-side (explicit null on update).
			{
				Config: testAccServiceAccountResourceUpdateConfigV2,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("anthropic_service_account.test", "name", "tf-acc-test-service-account-update"),
					resource.TestCheckNoResourceAttr("anthropic_service_account.test", "description"),
				),
			},
		},
	})
}
