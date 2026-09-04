// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package federation_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

// TestAccFederationRuleDataSource is a read-only smoke test. The
// anthropic_federation_rule *resource* and the anthropic_federation_rules
// *list* data source (which would normally chain to resolve a real ID, as
// organization_member does off organization_members) live on sibling,
// not-yet-merged branches (#137) and are not available on this branch's
// provider build. To still exercise a real rule end to end, this test uses
// the Anthropic SDK directly (not the Terraform provider) to list existing
// federation rules in the org and resolve one ID, then feeds that ID into
// the anthropic_federation_rule data source under test. If the org has no
// federation rules yet, the test skips rather than failing, since seeding
// one requires the sibling rule resource.
func TestAccFederationRuleDataSource(t *testing.T) {
	acctest.PreCheckOAuth(t)

	ruleID := resolveExistingFederationRuleID(t)
	if ruleID == "" {
		t.Skip("no federation rules exist in this organization yet; seeding one requires the anthropic_federation_rule resource, which lives on a sibling branch (#137)")
	}

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckOAuth(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: fmt.Sprintf(`
data "anthropic_federation_rule" "test" {
  id = %q
}
`, ruleID),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.anthropic_federation_rule.test", "id", ruleID),
					resource.TestCheckResourceAttrSet("data.anthropic_federation_rule.test", "name"),
					resource.TestCheckResourceAttrSet("data.anthropic_federation_rule.test", "issuer_id"),
					resource.TestCheckResourceAttrSet("data.anthropic_federation_rule.test", "oauth_scope"),
					resource.TestCheckResourceAttrSet("data.anthropic_federation_rule.test", "target.service_account_id"),
					resource.TestCheckResourceAttrSet("data.anthropic_federation_rule.test", "token_lifetime_seconds"),
					resource.TestCheckResourceAttrSet("data.anthropic_federation_rule.test", "created_at"),
				),
			},
		},
	})
}

// resolveExistingFederationRuleID lists federation rules directly via the SDK
// (bypassing the Terraform provider, since no list data source exists on
// this branch) and returns the first rule's ID, or "" if none exist or the
// request fails.
func resolveExistingFederationRuleID(t *testing.T) string {
	t.Helper()

	client := anthropic.NewClient(
		option.WithoutEnvironmentDefaults(),
		option.WithAuthToken(os.Getenv("ANTHROPIC_AUTH_TOKEN")),
	)

	page, err := client.Beta.Organization.Federation.Rules.List(context.Background(), anthropic.BetaOrganizationFederationRuleListParams{
		Limit: param.NewOpt(int64(1)),
	})
	if err != nil || page == nil || len(page.Data) == 0 {
		return ""
	}

	return page.Data[0].ID
}
