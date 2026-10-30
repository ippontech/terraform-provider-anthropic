// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package spendlimits_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
	"github.com/ippontech/terraform-provider-anthropic/internal/admin"
)

// preCheckSpendLimitsAcc gates every acceptance test of this package. Setting
// user limits needs a Claude Enterprise organization with a scoped Admin API
// key (read:/write:spend_limits); the terraform-tests organization is a
// Console one without the early access preview, so CI (which does not set the
// opt-in) always skips them. Set ANTHROPIC_SPEND_LIMITS_ACC=1 against a
// dedicated Enterprise test organization only: the resource test sets, then
// deletes, the daily limit of the organization's first member, adopting any
// daily override it already had.
func preCheckSpendLimitsAcc(t *testing.T) {
	acctest.PreCheckAdmin(t)
	if os.Getenv("ANTHROPIC_SPEND_LIMITS_ACC") == "" {
		t.Skip("skipping: ANTHROPIC_SPEND_LIMITS_ACC is not set. Spend limits need a Claude Enterprise organization " +
			"(or the Console early access preview); the terraform-tests organization has neither.")
	}
}

func testAccSpendLimitConfig(amount string) string {
	return fmt.Sprintf(`
data "anthropic_organization_members" "all" {}

resource "anthropic_spend_limit" "test" {
  scope = {
    type    = "user"
    user_id = data.anthropic_organization_members.all.members[0].id
  }
  period = "daily"
  amount = %q
}
`, amount)
}

func TestAccSpendLimitResource_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheckSpendLimitsAcc(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckSpendLimitDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccSpendLimitConfig("50000"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("anthropic_spend_limit.test", "amount", "50000"),
					resource.TestCheckResourceAttr("anthropic_spend_limit.test", "period", "daily"),
					resource.TestCheckResourceAttr("anthropic_spend_limit.test", "scope.type", "user"),
					resource.TestCheckResourceAttrSet("anthropic_spend_limit.test", "id"),
					resource.TestCheckResourceAttrSet("anthropic_spend_limit.test", "currency"),
				),
			},
			{
				Config: testAccSpendLimitConfig("75000"),
				Check:  resource.TestCheckResourceAttr("anthropic_spend_limit.test", "amount", "75000"),
			},
			{
				ResourceName:      "anthropic_spend_limit.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

// testAccCheckSpendLimitDestroyed asserts that every limit of the test is gone.
func testAccCheckSpendLimitDestroyed(s *terraform.State) error {
	client := admin.NewClient(os.Getenv("ANTHROPIC_ADMIN_API_KEY"))
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "anthropic_spend_limit" {
			continue
		}
		_, err := client.DoRequest(context.Background(), "GET", "/v1/organizations/spend_limits/"+rs.Primary.ID+"?beta=true", nil)
		if err == nil {
			return fmt.Errorf("spend limit %s still exists", rs.Primary.ID)
		}
		if !admin.IsNotFound(err) {
			return fmt.Errorf("checking spend limit %s: %w", rs.Primary.ID, err)
		}
	}
	return nil
}
