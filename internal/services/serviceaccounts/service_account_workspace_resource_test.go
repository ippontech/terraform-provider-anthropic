// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package serviceaccounts_test

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// Service account workspace acceptance tests require an org:admin OAuth
// bearer token (ANTHROPIC_AUTH_TOKEN): these endpoints reject API keys
// outright. No test org exists yet for org-level writes (the same blocker as
// #58) and CI has no durable org:admin token, so these run locally only —
// gated on acctest.PreCheckOAuth, which skips rather than fails when the
// token is absent.
//
// The public example this resource ships
// (examples/resources/service_account_workspace) chains anthropic_service_account,
// which is implemented on a sibling branch (see #137) and does not exist
// here. To keep this branch self-contained, the service account this test's
// membership targets is created directly through the SDK in test setup, not
// through that Terraform resource.

// newTestOAuthClient builds an SDK client authenticated with the org:admin
// OAuth bearer token, used for out-of-band fixture setup/teardown and for
// CheckDestroy.
func newTestOAuthClient() anthropic.Client {
	return anthropic.NewClient(option.WithAuthToken(os.Getenv("ANTHROPIC_AUTH_TOKEN")))
}

// setupServiceAccountFixture creates the service account a test's
// anthropic_service_account_workspace targets, and registers a t.Cleanup to
// archive it afterwards.
//
// Cleanup ordering matters: the membership itself is destroyed by the
// Terraform testing framework's automatic post-Steps destroy, which runs
// before t.Cleanup funcs, so by the time this archives the service account no
// membership still references it.
func setupServiceAccountFixture(t *testing.T) string {
	t.Helper()
	acctest.PreCheckOAuth(t)

	client := newTestOAuthClient()
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	account, err := client.Beta.Organization.ServiceAccounts.New(ctx, anthropic.BetaOrganizationServiceAccountNewParams{
		Name: fmt.Sprintf("tf-acc-svc-%s", suffix),
	})
	if err != nil {
		t.Fatalf("failed to create test service account: %s", err)
	}

	t.Cleanup(func() {
		if _, err := client.Beta.Organization.ServiceAccounts.Archive(ctx, account.ID, anthropic.BetaOrganizationServiceAccountArchiveParams{}); err != nil {
			t.Logf("cleanup: failed to archive test service account %s: %s", account.ID, err)
		}
	})

	return account.ID
}

// testAccCheckServiceAccountWorkspaceDestroyed verifies the membership was
// actually removed (hard delete, no archive concept here): the workspace must
// no longer show up in the service account's workspace list, or must have
// reverted to its implicit membership.
func testAccCheckServiceAccountWorkspaceDestroyed(s *terraform.State) error {
	client := newTestOAuthClient()
	ctx := context.Background()

	for _, rs := range s.RootModule().Resources {
		if rs.Type != "anthropic_service_account_workspace" {
			continue
		}
		serviceAccountID := rs.Primary.Attributes["service_account_id"]
		workspaceID := rs.Primary.Attributes["workspace_id"]

		iter := client.Beta.Organization.ServiceAccounts.Workspaces.ListAutoPaging(ctx, serviceAccountID, anthropic.BetaOrganizationServiceAccountWorkspaceListParams{})
		for iter.Next() {
			member := iter.Current()
			if member.WorkspaceID == workspaceID && !member.Implicit {
				return fmt.Errorf("service account workspace membership %s still exists as an explicit membership", rs.Primary.ID)
			}
		}
		if err := iter.Err(); err != nil {
			return fmt.Errorf("service account workspace membership %s: unable to verify destruction: %w", rs.Primary.ID, err)
		}
	}
	return nil
}

func testAccServiceAccountWorkspaceConfig(serviceAccountID, workspaceID, workspaceRole string) string {
	return fmt.Sprintf(`
resource "anthropic_service_account_workspace" "test" {
  service_account_id = %[1]q
  workspace_id        = %[2]q
  workspace_role      = %[3]q
}
`, serviceAccountID, workspaceID, workspaceRole)
}

func TestAccServiceAccountWorkspaceResource_basic(t *testing.T) {
	serviceAccountID := setupServiceAccountFixture(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckOAuth(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckServiceAccountWorkspaceDestroyed,
		Steps: []resource.TestStep{
			// Create and Read
			{
				Config: testAccServiceAccountWorkspaceConfig(serviceAccountID, acctest.TerraformTestsWorkspaceID, "workspace_developer"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_service_account_workspace.test", "id"),
					resource.TestCheckResourceAttr("anthropic_service_account_workspace.test", "service_account_id", serviceAccountID),
					resource.TestCheckResourceAttr("anthropic_service_account_workspace.test", "workspace_id", acctest.TerraformTestsWorkspaceID),
					resource.TestCheckResourceAttr("anthropic_service_account_workspace.test", "workspace_role", "workspace_developer"),
					resource.TestCheckResourceAttr("anthropic_service_account_workspace.test", "implicit", "false"),
					resource.TestCheckResourceAttrSet("anthropic_service_account_workspace.test", "created_by_actor_id"),
				),
			},
			// ImportState round-trip
			{
				ResourceName:      "anthropic_service_account_workspace.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

// TestAccServiceAccountWorkspaceResource_roleChangeReplaces verifies that
// changing workspace_role forces replacement, since the API has no update
// endpoint for this membership.
func TestAccServiceAccountWorkspaceResource_roleChangeReplaces(t *testing.T) {
	serviceAccountID := setupServiceAccountFixture(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckOAuth(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckServiceAccountWorkspaceDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccServiceAccountWorkspaceConfig(serviceAccountID, acctest.TerraformTestsWorkspaceID, "workspace_developer"),
				Check:  resource.TestCheckResourceAttr("anthropic_service_account_workspace.test", "workspace_role", "workspace_developer"),
			},
			{
				Config: testAccServiceAccountWorkspaceConfig(serviceAccountID, acctest.TerraformTestsWorkspaceID, "workspace_admin"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("anthropic_service_account_workspace.test", plancheck.ResourceActionReplace),
					},
				},
				Check: resource.TestCheckResourceAttr("anthropic_service_account_workspace.test", "workspace_role", "workspace_admin"),
			},
		},
	})
}
