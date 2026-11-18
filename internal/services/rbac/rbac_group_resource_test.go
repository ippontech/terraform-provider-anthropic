// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package rbac_test

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

// preCheckEnterpriseAcc gates every acceptance test of this package. The RBAC
// endpoints exist only for Claude Enterprise organizations and need a scoped
// Admin API key; the terraform-tests organization is a Console one, so CI
// (which does not set the opt-in) always skips them. Set
// ANTHROPIC_ENTERPRISE_ACC=1 with an Enterprise key created for all linked
// organizations (read:/write:rbac_groups, read:members) to run them live.
func preCheckEnterpriseAcc(t *testing.T) {
	acctest.PreCheckAdmin(t)
	if os.Getenv("ANTHROPIC_ENTERPRISE_ACC") == "" {
		t.Skip("skipping: ANTHROPIC_ENTERPRISE_ACC is not set. The RBAC endpoints are Claude Enterprise only and " +
			"the terraform-tests organization is a Console organization.")
	}
}

func testAccRBACGroupConfig(name string) string {
	return fmt.Sprintf(`
resource "anthropic_rbac_group" "test" {
  name = %q
}
`, name)
}

func TestAccRBACGroupResource_basic(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { preCheckEnterpriseAcc(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckRBACGroupDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccRBACGroupConfig("tf-acc-rbac-group"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("anthropic_rbac_group.test", "name", "tf-acc-rbac-group"),
					resource.TestCheckResourceAttr("anthropic_rbac_group.test", "source_type", "direct"),
					resource.TestCheckResourceAttrSet("anthropic_rbac_group.test", "id"),
					resource.TestCheckResourceAttrSet("anthropic_rbac_group.test", "created_at"),
				),
			},
			{
				Config: testAccRBACGroupConfig("tf-acc-rbac-group-renamed"),
				Check:  resource.TestCheckResourceAttr("anthropic_rbac_group.test", "name", "tf-acc-rbac-group-renamed"),
			},
			{
				ResourceName:      "anthropic_rbac_group.test",
				ImportState:       true,
				ImportStateVerify: true,
				// role_ids can read back null on a degraded read.
				ImportStateVerifyIgnore: []string{"role_ids"},
			},
		},
	})
}

// testAccCheckRBACGroupDestroyed asserts that every group of the test is gone.
func testAccCheckRBACGroupDestroyed(s *terraform.State) error {
	client := admin.NewClient(os.Getenv("ANTHROPIC_ADMIN_API_KEY"))
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "anthropic_rbac_group" {
			continue
		}
		_, err := client.DoRequest(context.Background(), "GET", "/v1/organizations/rbac_groups/"+rs.Primary.ID+"?beta=true", nil)
		if err == nil {
			return fmt.Errorf("RBAC group %s still exists", rs.Primary.ID)
		}
		if !admin.IsNotFound(err) {
			return fmt.Errorf("checking RBAC group %s: %w", rs.Primary.ID, err)
		}
	}
	return nil
}
