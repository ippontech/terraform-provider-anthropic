// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation_test

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	"github.com/hashicorp/terraform-plugin-testing/tfjsonpath"

	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

const fakeRuleName = "anthropic_federation_rule.test"

func fakeFederationRuleConfig(condition, serviceAccountID string) string {
	return fmt.Sprintf(`
resource "anthropic_federation_rule" "test" {
  name         = "ci-deploy"
  issuer_id    = "fdis_001"
  oauth_scope  = "workspace:developer"
  workspace_id = "wrkspc_001"

  match = {
    condition = %q
  }
  target = {
    service_account_id = %q
  }
}
`, condition, serviceAccountID)
}

// #349: an in-place update (only match.condition changes) keeps
// target.service_account_name, which the update response omits. The
// resource maps the read that confirms the write instead.
func TestFederationRuleResource_updateKeepsServiceAccountName(t *testing.T) {
	newFakeFederationAPI(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeFederationRuleConfig("claims.ref == 'refs/heads/main'", "svac_001"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(fakeRuleName, "target.service_account_name", "sa-one"),
					resource.TestCheckResourceAttr(fakeRuleName, "issuer_name", "github"),
				),
			},
			{
				Config: fakeFederationRuleConfig("claims.ref == 'refs/heads/release'", "svac_001"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction(fakeRuleName, plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(fakeRuleName, "match.condition", "claims.ref == 'refs/heads/release'"),
					resource.TestCheckResourceAttr(fakeRuleName, "target.service_account_name", "sa-one"),
				),
			},
		},
	})
}

// When the read after the update fails (here a 403, which ends the
// best-effort wait at once), the write response is mapped with the planned
// name, since it names none for the same service account.
func TestFederationRuleResource_updateKeepsServiceAccountNameWhenReadBackFails(t *testing.T) {
	fake := newFakeFederationAPI(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeFederationRuleConfig("claims.ref == 'refs/heads/main'", "svac_001"),
			},
			{
				PreConfig: func() {
					fake.with(func(f *fakeFederationAPI) { f.failReadAfterUpdate = true })
				},
				Config: fakeFederationRuleConfig("claims.ref == 'refs/heads/release'", "svac_001"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(fakeRuleName, "target.service_account_name", "sa-one"),
					func(*terraform.State) error {
						var failed int
						fake.with(func(f *fakeFederationAPI) { failed = f.failedReads })
						if failed != 1 {
							return fmt.Errorf("read-back failures served = %d, want 1", failed)
						}
						return nil
					},
				),
			},
		},
	})
}

// Moving the rule to another service account plans the name as unknown and
// reads the new account's name, instead of carrying the old one in the plan.
func TestFederationRuleResource_updateChangesServiceAccount(t *testing.T) {
	newFakeFederationAPI(t)

	resource.UnitTest(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fakeFederationRuleConfig("claims.ref == 'refs/heads/main'", "svac_001"),
			},
			{
				Config: fakeFederationRuleConfig("claims.ref == 'refs/heads/main'", "svac_002"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectUnknownValue(fakeRuleName, tfjsonpath.New("target").AtMapKey("service_account_name")),
					},
				},
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr(fakeRuleName, "target.service_account_id", "svac_002"),
					resource.TestCheckResourceAttr(fakeRuleName, "target.service_account_name", "sa-two"),
				),
			},
		},
	})
}
