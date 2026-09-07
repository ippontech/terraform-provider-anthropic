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
)

// This is a smoke acceptance test: it exercises the real API end to end, but
// (like other WIF acceptance tests) requires an org:admin OAuth bearer token
// (ANTHROPIC_AUTH_TOKEN), which no test org and no durable CI credential
// currently provide (same blocker as #58). It is gated on acctest.PreCheckOAuth,
// which skips rather than fails when the token is absent, so it runs locally
// only until #137 lands a way to mint one in CI.
//
// The public example this data source ships
// (examples/data-sources/service_account_workspaces) chains
// data.anthropic_service_accounts, which is implemented on a sibling branch
// (see #137) and does not exist here. To keep this branch self-contained, the
// service account this test lists workspaces for is created directly through
// the SDK in test setup, not through that data source.

// newTestOAuthClientForDataSource builds an SDK client authenticated with the
// org:admin OAuth bearer token, used for out-of-band fixture setup/teardown.
// Named distinctly from any equivalent helper a sibling WIF branch (e.g. the
// anthropic_service_account_workspace resource) might add to this same
// external test package, so the two can coexist once both branches merge.
func newTestOAuthClientForDataSource() anthropic.Client {
	return anthropic.NewClient(option.WithAuthToken(os.Getenv("ANTHROPIC_AUTH_TOKEN")))
}

// setupServiceAccountFixtureForDataSource creates the service account this
// test's anthropic_service_account_workspaces data source lists, and
// registers a t.Cleanup to archive it afterwards.
func setupServiceAccountFixtureForDataSource(t *testing.T) string {
	t.Helper()
	acctest.PreCheckOAuth(t)

	client := newTestOAuthClientForDataSource()
	ctx := context.Background()
	suffix := fmt.Sprintf("%d", time.Now().UnixNano())

	account, err := client.Beta.Organization.ServiceAccounts.New(ctx, anthropic.BetaOrganizationServiceAccountNewParams{
		Name: fmt.Sprintf("tf-acc-svcws-%s", suffix),
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

func testAccServiceAccountWorkspacesDataSourceConfig(serviceAccountID string) string {
	return fmt.Sprintf(`
data "anthropic_service_account_workspaces" "test" {
  service_account_id = %[1]q
}
`, serviceAccountID)
}

// TestAccServiceAccountWorkspacesDataSource_basic asserts that a freshly
// created service account, which has no explicit workspace membership yet,
// already shows up with at least its implicit default-workspace membership.
func TestAccServiceAccountWorkspacesDataSource_basic(t *testing.T) {
	serviceAccountID := setupServiceAccountFixtureForDataSource(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckOAuth(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccServiceAccountWorkspacesDataSourceConfig(serviceAccountID),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("data.anthropic_service_account_workspaces.test", "service_account_id", serviceAccountID),
					resource.TestCheckResourceAttrSet("data.anthropic_service_account_workspaces.test", "workspaces.#"),
					resource.TestCheckResourceAttr("data.anthropic_service_account_workspaces.test", "workspaces.0.implicit", "true"),
					resource.TestCheckResourceAttrSet("data.anthropic_service_account_workspaces.test", "workspaces.0.workspace_id"),
					resource.TestCheckResourceAttrSet("data.anthropic_service_account_workspaces.test", "workspaces.0.workspace_role"),
					resource.TestCheckResourceAttrSet("data.anthropic_service_account_workspaces.test", "workspaces.0.created_by_actor_id"),
				),
			},
		},
	})
}
