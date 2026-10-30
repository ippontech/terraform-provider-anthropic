// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package vaults_test

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"

	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

// Live tests of the provider's workspace_id argument, run with a personal or
// service account API key that is not scoped to a single workspace (see
// acctest.PreCheckMultiWorkspace). The argument has no environment fallback,
// so an ANTHROPIC_WORKSPACE_ID exported for federation cannot interfere with
// the no-workspace case below. Vaults are the probe: free to manage,
// workspace-scoped, and already covered by CRUD acceptance tests, so what is
// asserted here is only the routing — the API rejects the key without the
// header, and the vault lands in the workspace the header names and in no
// other. Each test cleans up through a client routed to the same workspace.

// multiWorkspaceProviderConfig returns a provider block authenticated with the
// multi-workspace key, with workspace_id set when workspaceID is not empty.
func multiWorkspaceProviderConfig(workspaceID string) string {
	cfg := fmt.Sprintf("provider \"anthropic\" {\n  api_key = %q\n", os.Getenv(acctest.MultiWorkspaceAPIKeyEnv))
	if workspaceID != "" {
		cfg += fmt.Sprintf("  workspace_id = %q\n", workspaceID)
	}
	return cfg + "}\n"
}

func multiWorkspaceVaultConfig(providerBlock, displayName string) string {
	return providerBlock + fmt.Sprintf(`
resource "anthropic_vault" "test" {
  display_name = %q
}
`, displayName)
}

// otherTestWorkspace returns the test workspace that is not workspaceID.
func otherTestWorkspace(workspaceID string) string {
	if workspaceID == acctest.TerraformTestsWorkspaceID {
		return acctest.TerraformTests2WorkspaceID
	}
	return acctest.TerraformTestsWorkspaceID
}

// testAccCheckVaultInWorkspace asserts the vault is readable through the
// workspace the header names and absent (404) through the other test
// workspace: the header routed the write, not the key's membership.
func testAccCheckVaultInWorkspace(workspaceID string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources["anthropic_vault.test"]
		if !ok {
			return fmt.Errorf("anthropic_vault.test not found in state")
		}
		id := rs.Primary.ID
		ctx := context.Background()
		here := acctest.NewMultiWorkspaceClient(workspaceID)
		if _, err := here.Beta.Vaults.Get(ctx, id, anthropic.BetaVaultGetParams{}); err != nil {
			return fmt.Errorf("vault %s not readable in workspace %s: %w", id, workspaceID, err)
		}
		other := otherTestWorkspace(workspaceID)
		elsewhere := acctest.NewMultiWorkspaceClient(other)
		_, err := elsewhere.Beta.Vaults.Get(ctx, id, anthropic.BetaVaultGetParams{})
		if err == nil {
			return fmt.Errorf("vault %s is also visible from workspace %s: the header did not scope the write", id, other)
		}
		if !isNotFoundError(err) {
			return fmt.Errorf("reading vault %s from workspace %s: want 404, got %w", id, other, err)
		}
		return nil
	}
}

func testAccCheckVaultDestroyedInWorkspace(workspaceID string) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		client := *acctest.NewMultiWorkspaceClient(workspaceID)
		for _, rs := range s.RootModule().Resources {
			if rs.Type != "anthropic_vault" {
				continue
			}
			if err := awaitVaultGone(client, rs.Primary.ID); err != nil {
				return err
			}
		}
		return nil
	}
}

// Without workspace_id, an identity-linked key not bound to one workspace is
// rejected by the API with a 400 naming the missing header (#283).
func TestAccVaultResource_multiWorkspaceKeyRequiresWorkspaceID(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckMultiWorkspace(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config:      multiWorkspaceVaultConfig(multiWorkspaceProviderConfig(""), "tf-acc-test-vault-no-workspace"),
				ExpectError: regexp.MustCompile(`anthropic-workspace-id`),
			},
		},
	})
}

// With workspace_id, the same key creates the vault in the named workspace,
// whichever of the two test workspaces it is, and nowhere else.
func TestAccVaultResource_multiWorkspaceKeyRoutesToWorkspaceID(t *testing.T) {
	for name, workspaceID := range map[string]string{
		"terraform-tests":   acctest.TerraformTestsWorkspaceID,
		"terraform-tests-2": acctest.TerraformTests2WorkspaceID,
	} {
		t.Run(name, func(t *testing.T) {
			resource.Test(t, resource.TestCase{
				PreCheck:                 func() { acctest.PreCheckMultiWorkspace(t) },
				ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
				CheckDestroy:             testAccCheckVaultDestroyedInWorkspace(workspaceID),
				Steps: []resource.TestStep{
					{
						Config: multiWorkspaceVaultConfig(multiWorkspaceProviderConfig(workspaceID), "tf-acc-test-vault-"+name),
						Check: resource.ComposeTestCheckFunc(
							resource.TestCheckResourceAttrSet("anthropic_vault.test", "id"),
							testAccCheckVaultInWorkspace(workspaceID),
						),
					},
				},
			})
		})
	}
}
