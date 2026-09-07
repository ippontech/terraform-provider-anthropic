// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package serviceaccounts_test

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

// TestAccServiceAccountDataSource is a read-only smoke test for the single
// service account data source.
//
// This branch is isolated from the sibling WIF PRs (#137 series): neither the
// anthropic_service_account resource nor the anthropic_service_accounts list
// data source exist here, so there is nothing to chain an ID off in
// Terraform. Instead the fixture is created directly through the SDK (the
// same org:admin OAuth bearer credential the provider itself uses) before the
// Terraform steps run, and archived afterward — there is no hard-delete
// endpoint for service accounts.
func TestAccServiceAccountDataSource(t *testing.T) {
	acctest.PreCheckOAuth(t)

	client := anthropic.NewClient(option.WithAuthToken(os.Getenv("ANTHROPIC_AUTH_TOKEN")))

	sa, err := client.Beta.Organization.ServiceAccounts.New(context.Background(), anthropic.BetaOrganizationServiceAccountNewParams{
		Name: "tf-acc-service-account-ds",
	})
	if err != nil {
		t.Fatalf("failed to create fixture service account: %s", err)
	}
	t.Cleanup(func() {
		if _, err := client.Beta.Organization.ServiceAccounts.Archive(context.Background(), sa.ID, anthropic.BetaOrganizationServiceAccountArchiveParams{}); err != nil {
			t.Errorf("failed to archive fixture service account %s: %s", sa.ID, err)
		}
	})

	config := fmt.Sprintf(`
data "anthropic_service_account" "test" {
  id = %q
}
`, sa.ID)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.anthropic_service_account.test", "id", sa.ID),
					resource.TestCheckResourceAttr("data.anthropic_service_account.test", "name", "tf-acc-service-account-ds"),
					resource.TestCheckResourceAttr("data.anthropic_service_account.test", "organization_role", "developer"),
					resource.TestCheckResourceAttrSet("data.anthropic_service_account.test", "created_at"),
					resource.TestCheckResourceAttrSet("data.anthropic_service_account.test", "created_by_actor_id"),
					resource.TestCheckNoResourceAttr("data.anthropic_service_account.test", "archived_at"),
				),
			},
		},
	})
}
