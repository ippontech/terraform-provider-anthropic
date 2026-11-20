// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package vaults_test

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
	acctest "github.com/ippontech/terraform-provider-anthropic/internal/acctest"
)

// Live tests of the api_federation provider block: the standard client runs
// on a workspace:developer token the provider mints from the job's GitHub
// Actions OIDC token, with no API key (see acctest.PreCheckAPIFederation for
// where they run). Vaults are the probe, as for the workspace_id argument:
// free to manage, workspace-scoped, and a Managed Agents endpoint, which the
// WIF reference (platform.claude.com/docs/en/manage-claude/wif-reference,
// re-read 2026-11-10) lists under workspace:developer. The first test runs a
// full create, update and destroy through the federated client; the other two
// check in which workspace the vault lands.

// apiFederationProviderConfig returns a provider block whose standard client
// is federated through ruleID. blockWorkspaceID sets api_federation.workspace_id
// and providerWorkspaceID the provider-level workspace_id, each when not empty.
func apiFederationProviderConfig(ruleID, blockWorkspaceID, providerWorkspaceID string) string {
	command := make([]string, len(acctest.GitHubIdentityTokenCommand))
	for i, arg := range acctest.GitHubIdentityTokenCommand {
		command[i] = fmt.Sprintf("%q", arg)
	}
	var b strings.Builder
	b.WriteString("provider \"anthropic\" {\n")
	if providerWorkspaceID != "" {
		fmt.Fprintf(&b, "  workspace_id = %q\n", providerWorkspaceID)
	}
	b.WriteString("  api_federation = {\n")
	fmt.Fprintf(&b, "    organization_id        = %q\n", acctest.TerraformTestsOrganizationID)
	fmt.Fprintf(&b, "    federation_rule_id     = %q\n", ruleID)
	fmt.Fprintf(&b, "    service_account_id     = %q\n", acctest.TerraformTestsServiceAccountID)
	if blockWorkspaceID != "" {
		fmt.Fprintf(&b, "    workspace_id           = %q\n", blockWorkspaceID)
	}
	fmt.Fprintf(&b, "    identity_token_command = [%s]\n", strings.Join(command, ", "))
	b.WriteString("  }\n}\n")
	return b.String()
}

// testAccCheckVaultDestroyedWithAPIKey checks destruction through the
// standard key, which is scoped to terraform-tests, where the single-workspace
// rule's tokens land.
func testAccCheckVaultDestroyedWithAPIKey(s *terraform.State) error {
	client := *acctest.NewAPIKeyClient()
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

// A rule enabled for one workspace: no workspace_id anywhere, and the token
// creates, updates and deletes a vault in terraform-tests. Each step
// configures the provider again, so each runs its own exchange with a fresh
// GitHub token.
func TestAccVaultResource_apiFederation(t *testing.T) {
	providerBlock := apiFederationProviderConfig(acctest.TerraformTestsFederationRuleID, "", "")
	resource.Test(t, resource.TestCase{
		PreCheck: func() {
			acctest.PreCheckAPIFederation(t)
			acctest.PreCheck(t)
		},
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckVaultDestroyedWithAPIKey,
		Steps: []resource.TestStep{
			{
				Config: providerVaultConfig(providerBlock, "tf-acc-test-vault-api-federation"),
				Check: resource.ComposeTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_vault.test", "id"),
					resource.TestCheckResourceAttr("anthropic_vault.test", "display_name", "tf-acc-test-vault-api-federation"),
				),
			},
			{
				Config: providerVaultConfig(providerBlock, "tf-acc-test-vault-api-federation-renamed"),
				Check:  resource.TestCheckResourceAttr("anthropic_vault.test", "display_name", "tf-acc-test-vault-api-federation-renamed"),
			},
		},
	})
}

// A rule enabled for both test workspaces requires a workspace at exchange
// time: without one, the exchange is refused with a 401 (the WIF reference,
// re-read 2026-11-10, documents the reason workspace_id_required, but the
// live body is a generic authentication_error carrying no reason). The
// exchange is lazy, so the error is the vault create's. Only the status is
// matched, in the wording of the SDK's token middleware
// (internal/auth.OAuthTokenError, "oauth token request failed (status 401)"),
// which the provider's option.WithFederationTokenProvider path goes through;
// config's FederationExchangeError ("federation exchange failed") never
// reaches it. Terraform word-wraps diagnostic details, and the first live run
// split this phrase after "request", so every space in the pattern is \s+.
// A 401 for another cause (subject, audience) is ruled out by
// TestAccVaultResource_apiFederationSelectsWorkspace, which exchanges
// against the same rule with the same token and must pass.
func TestAccVaultResource_apiFederationMultiWorkspaceRuleRequiresWorkspace(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheckAPIFederation(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: providerVaultConfig(
					apiFederationProviderConfig(acctest.TerraformTestsMultiWorkspaceFederationRuleID, "", ""),
					"tf-acc-test-vault-api-federation-no-workspace"),
				ExpectError: regexp.MustCompile(`oauth\s+token\s+request\s+failed\s+\(status\s+401\)`),
			},
		},
	})
}

// With the multi-workspace rule, the workspace the provider passes to the
// exchange decides where the vault lands, whether it comes from
// api_federation.workspace_id or from the provider-level workspace_id, and
// for either test workspace. The multi-workspace key checks out of band that
// the vault is in that workspace and in no other.
func TestAccVaultResource_apiFederationSelectsWorkspace(t *testing.T) {
	for _, tc := range []struct {
		name                              string
		workspaceID                       string
		blockWorkspace, providerWorkspace bool
	}{
		{name: "block-terraform-tests", workspaceID: acctest.TerraformTestsWorkspaceID, blockWorkspace: true},
		{name: "block-terraform-tests-2", workspaceID: acctest.TerraformTests2WorkspaceID, blockWorkspace: true},
		{name: "provider-terraform-tests-2", workspaceID: acctest.TerraformTests2WorkspaceID, providerWorkspace: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var blockWorkspaceID, providerWorkspaceID string
			if tc.blockWorkspace {
				blockWorkspaceID = tc.workspaceID
			}
			if tc.providerWorkspace {
				providerWorkspaceID = tc.workspaceID
			}
			resource.Test(t, resource.TestCase{
				PreCheck: func() {
					acctest.PreCheckAPIFederation(t)
					acctest.PreCheckMultiWorkspace(t)
				},
				ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
				CheckDestroy:             testAccCheckVaultDestroyedInWorkspace(tc.workspaceID),
				Steps: []resource.TestStep{
					{
						Config: providerVaultConfig(
							apiFederationProviderConfig(acctest.TerraformTestsMultiWorkspaceFederationRuleID, blockWorkspaceID, providerWorkspaceID),
							"tf-acc-test-vault-api-federation-"+tc.name),
						Check: resource.ComposeTestCheckFunc(
							resource.TestCheckResourceAttrSet("anthropic_vault.test", "id"),
							testAccCheckVaultInWorkspace(tc.workspaceID),
						),
					},
				},
			})
		})
	}
}
