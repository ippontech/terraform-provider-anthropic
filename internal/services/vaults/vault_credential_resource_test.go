// Copyright (c) Ippon
// SPDX-License-Identifier: MPL-2.0

package vaults_test

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
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// Credentials are stored as provided and are NOT validated until session
// runtime (per the Vaults API docs), so acceptance tests can create them with
// fabricated secret material and unreachable MCP server URLs — the create/read/
// update/delete lifecycle exercises fully without ever starting a session.

func testAccCheckVaultCredentialDestroyed(s *terraform.State) error {
	client := anthropic.NewClient(option.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")))
	for _, rs := range s.RootModule().Resources {
		if rs.Type != "anthropic_vault_credential" {
			continue
		}
		// 404 covers both the credential and its parent vault being deleted.
		err := awaitGone("vault credential", rs.Primary.ID, func(ctx context.Context) error {
			_, err := client.Beta.Vaults.Credentials.Get(ctx, rs.Primary.ID, anthropic.BetaVaultCredentialGetParams{
				VaultID: rs.Primary.Attributes["vault_id"],
			})
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

// testAccVaultCredentialImportIDFunc builds the composite import ID
// "<vault_id>:<credential_id>" from state.
func testAccVaultCredentialImportIDFunc(name string) resource.ImportStateIdFunc {
	return func(s *terraform.State) (string, error) {
		rs, ok := s.RootModule().Resources[name]
		if !ok {
			return "", fmt.Errorf("resource not found in state: %s", name)
		}
		return fmt.Sprintf("%s:%s", rs.Primary.Attributes["vault_id"], rs.Primary.ID), nil
	}
}

const testAccVaultCredentialStaticBearerConfig = `
resource "anthropic_vault" "test" {
  display_name = "tf-acc-test-vault-cred-bearer"
}

resource "anthropic_vault_credential" "bearer" {
  vault_id       = anthropic_vault.test.id
  type           = "static_bearer"
  display_name   = "tf-acc bearer"
  mcp_server_url = "https://mcp.example.com/bearer"

  token            = "tf-acc-fake-bearer-token"
  token_wo_version = 1

  metadata = {
    purpose = "acctest"
  }
}
`

func TestAccVaultCredentialResource_staticBearer(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckVaultCredentialDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccVaultCredentialStaticBearerConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_vault_credential.bearer", "id"),
					resource.TestCheckResourceAttrPair("anthropic_vault_credential.bearer", "vault_id", "anthropic_vault.test", "id"),
					resource.TestCheckResourceAttr("anthropic_vault_credential.bearer", "type", "static_bearer"),
					resource.TestCheckResourceAttr("anthropic_vault_credential.bearer", "display_name", "tf-acc bearer"),
					resource.TestCheckResourceAttr("anthropic_vault_credential.bearer", "mcp_server_url", "https://mcp.example.com/bearer"),
					resource.TestCheckResourceAttr("anthropic_vault_credential.bearer", "credential_type", "vault_credential"),
					resource.TestCheckResourceAttr("anthropic_vault_credential.bearer", "metadata.purpose", "acctest"),
					resource.TestCheckResourceAttrSet("anthropic_vault_credential.bearer", "created_at"),
					// Write-only secret must never be persisted to state.
					resource.TestCheckNoResourceAttr("anthropic_vault_credential.bearer", "token"),
				),
			},
			{
				ResourceName:            "anthropic_vault_credential.bearer",
				ImportState:             true,
				ImportStateIdFunc:       testAccVaultCredentialImportIDFunc("anthropic_vault_credential.bearer"),
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"token", "token_wo_version", "archive_on_destroy"},
			},
		},
	})
}

const testAccVaultCredentialEnvVarConfig = `
resource "anthropic_vault" "test" {
  display_name = "tf-acc-test-vault-cred-envvar"
}

resource "anthropic_vault_credential" "env_var" {
  vault_id     = anthropic_vault.test.id
  type         = "environment_variable"
  display_name = "tf-acc env var"

  secret_name      = "TF_ACC_API_KEY"
  secret_value     = "tf-acc-fake-secret-value"
  token_wo_version = 1

  networking = {
    mode          = "limited"
    allowed_hosts = ["api.example.com", "*.cdn.example.com"]
  }
}
`

func TestAccVaultCredentialResource_environmentVariable(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckVaultCredentialDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccVaultCredentialEnvVarConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_vault_credential.env_var", "id"),
					resource.TestCheckResourceAttr("anthropic_vault_credential.env_var", "type", "environment_variable"),
					resource.TestCheckResourceAttr("anthropic_vault_credential.env_var", "secret_name", "TF_ACC_API_KEY"),
					resource.TestCheckResourceAttr("anthropic_vault_credential.env_var", "networking.mode", "limited"),
					resource.TestCheckResourceAttr("anthropic_vault_credential.env_var", "networking.allowed_hosts.0", "api.example.com"),
					resource.TestCheckResourceAttr("anthropic_vault_credential.env_var", "networking.allowed_hosts.1", "*.cdn.example.com"),
					// mcp_server_url must be absent for environment_variable.
					resource.TestCheckNoResourceAttr("anthropic_vault_credential.env_var", "mcp_server_url"),
					resource.TestCheckNoResourceAttr("anthropic_vault_credential.env_var", "secret_value"),
				),
			},
			{
				ResourceName:            "anthropic_vault_credential.env_var",
				ImportState:             true,
				ImportStateIdFunc:       testAccVaultCredentialImportIDFunc("anthropic_vault_credential.env_var"),
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"secret_value", "token_wo_version", "archive_on_destroy"},
			},
		},
	})
}

const testAccVaultCredentialMCPOAuthConfig = `
resource "anthropic_vault" "test" {
  display_name = "tf-acc-test-vault-cred-oauth"
}

resource "anthropic_vault_credential" "oauth" {
  vault_id       = anthropic_vault.test.id
  type           = "mcp_oauth"
  display_name   = "tf-acc oauth"
  mcp_server_url = "https://mcp.example.com/oauth"

  access_token     = "tf-acc-fake-access-token"
  expires_at       = "2099-12-31T23:59:59Z"
  token_wo_version = 1

  refresh = {
    client_id      = "tf-acc-client-id"
    token_endpoint = "https://auth.example.com/oauth/token"
    scope          = "read write"
    refresh_token  = "tf-acc-fake-refresh-token"

    token_endpoint_auth = {
      type          = "client_secret_post"
      client_secret = "tf-acc-fake-client-secret"
    }
  }
}
`

func TestAccVaultCredentialResource_mcpOAuth(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckVaultCredentialDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccVaultCredentialMCPOAuthConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_vault_credential.oauth", "id"),
					resource.TestCheckResourceAttr("anthropic_vault_credential.oauth", "type", "mcp_oauth"),
					resource.TestCheckResourceAttr("anthropic_vault_credential.oauth", "mcp_server_url", "https://mcp.example.com/oauth"),
					resource.TestCheckResourceAttr("anthropic_vault_credential.oauth", "refresh.client_id", "tf-acc-client-id"),
					resource.TestCheckResourceAttr("anthropic_vault_credential.oauth", "refresh.token_endpoint", "https://auth.example.com/oauth/token"),
					resource.TestCheckResourceAttr("anthropic_vault_credential.oauth", "refresh.token_endpoint_auth.type", "client_secret_post"),
					// Write-only secrets must never be persisted to state.
					resource.TestCheckNoResourceAttr("anthropic_vault_credential.oauth", "access_token"),
					resource.TestCheckNoResourceAttr("anthropic_vault_credential.oauth", "refresh.refresh_token"),
					resource.TestCheckNoResourceAttr("anthropic_vault_credential.oauth", "refresh.token_endpoint_auth.client_secret"),
				),
			},
			{
				ResourceName:      "anthropic_vault_credential.oauth",
				ImportState:       true,
				ImportStateIdFunc: testAccVaultCredentialImportIDFunc("anthropic_vault_credential.oauth"),
				ImportStateVerify: true,
				ImportStateVerifyIgnore: []string{
					"access_token", "token_wo_version", "archive_on_destroy",
					"refresh.refresh_token", "refresh.token_endpoint_auth.client_secret",
				},
			},
		},
	})
}

const testAccVaultCredentialRotateConfigV1 = `
resource "anthropic_vault" "test" {
  display_name = "tf-acc-test-vault-cred-rotate"
}

resource "anthropic_vault_credential" "bearer" {
  vault_id       = anthropic_vault.test.id
  type           = "static_bearer"
  display_name   = "tf-acc rotate v1"
  mcp_server_url = "https://mcp.example.com/rotate"

  token            = "tf-acc-token-v1"
  token_wo_version = 1

  metadata = {
    stage = "one"
    keep  = "yes"
  }
}
`

// V2 rotates the secret (token_wo_version bump + new token), renames, changes one
// metadata value and drops another — exercising secret rotation, display_name
// update, and metadata PATCH/clear together.
const testAccVaultCredentialRotateConfigV2 = `
resource "anthropic_vault" "test" {
  display_name = "tf-acc-test-vault-cred-rotate"
}

resource "anthropic_vault_credential" "bearer" {
  vault_id       = anthropic_vault.test.id
  type           = "static_bearer"
  display_name   = "tf-acc rotate v2"
  mcp_server_url = "https://mcp.example.com/rotate"

  token            = "tf-acc-token-v2"
  token_wo_version = 2

  metadata = {
    stage = "two"
  }
}
`

func TestAccVaultCredentialResource_rotateAndUpdate(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckVaultCredentialDestroyed,
		Steps: []resource.TestStep{
			{
				Config: testAccVaultCredentialRotateConfigV1,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("anthropic_vault_credential.bearer", "display_name", "tf-acc rotate v1"),
					resource.TestCheckResourceAttr("anthropic_vault_credential.bearer", "token_wo_version", "1"),
					resource.TestCheckResourceAttr("anthropic_vault_credential.bearer", "metadata.stage", "one"),
					resource.TestCheckResourceAttr("anthropic_vault_credential.bearer", "metadata.keep", "yes"),
				),
			},
			{
				Config: testAccVaultCredentialRotateConfigV2,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("anthropic_vault_credential.bearer", "display_name", "tf-acc rotate v2"),
					resource.TestCheckResourceAttr("anthropic_vault_credential.bearer", "token_wo_version", "2"),
					resource.TestCheckResourceAttr("anthropic_vault_credential.bearer", "metadata.stage", "two"),
					// "keep" was removed in config and must be cleared server-side.
					resource.TestCheckNoResourceAttr("anthropic_vault_credential.bearer", "metadata.keep"),
				),
			},
		},
	})
}

// The vault also archives on destroy so it survives (archived, not deleted) and
// the credential remains queryable to confirm its archival; deleting a vault
// would hard-delete and cascade-delete the credential, leaving nothing to check.
const testAccVaultCredentialArchiveOnDestroyConfig = `
resource "anthropic_vault" "test" {
  display_name       = "tf-acc-test-vault-cred-archive"
  archive_on_destroy = true
}

resource "anthropic_vault_credential" "bearer" {
  vault_id       = anthropic_vault.test.id
  type           = "static_bearer"
  display_name   = "tf-acc archive"
  mcp_server_url = "https://mcp.example.com/archive"

  token              = "tf-acc-fake-token"
  token_wo_version   = 1
  archive_on_destroy = true
}
`

// testAccCheckVaultCredentialArchivedAndCleanup verifies both the credential and
// its vault were archived (not hard-deleted), then permanently deletes the vault
// (which cascade-deletes the credential) to avoid dangling resources.
func testAccCheckVaultCredentialArchivedAndCleanup(s *terraform.State) error {
	client := anthropic.NewClient(option.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")))
	var vaultID string

	for _, rs := range s.RootModule().Resources {
		switch rs.Type {
		case "anthropic_vault_credential":
			vaultIDAttr := rs.Primary.Attributes["vault_id"]
			err := awaitArchived("credential", rs.Primary.ID, func(ctx context.Context) (time.Time, error) {
				cred, err := client.Beta.Vaults.Credentials.Get(ctx, rs.Primary.ID, anthropic.BetaVaultCredentialGetParams{
					VaultID: vaultIDAttr,
				})
				if err != nil {
					return time.Time{}, err
				}
				return cred.ArchivedAt, nil
			})
			if err != nil {
				return err
			}
		case "anthropic_vault":
			err := awaitArchived("vault", rs.Primary.ID, func(ctx context.Context) (time.Time, error) {
				vault, err := client.Beta.Vaults.Get(ctx, rs.Primary.ID, anthropic.BetaVaultGetParams{})
				if err != nil {
					return time.Time{}, err
				}
				return vault.ArchivedAt, nil
			})
			if err != nil {
				return err
			}
			vaultID = rs.Primary.ID
		}
	}

	if vaultID != "" {
		if _, err := client.Beta.Vaults.Delete(context.Background(), vaultID, anthropic.BetaVaultDeleteParams{}); err != nil {
			return fmt.Errorf("cleanup: unable to delete archived vault %s: %w", vaultID, err)
		}
	}
	return nil
}

func TestAccVaultCredentialResource_archiveOnDestroy(t *testing.T) {
	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { acctest.PreCheck(t) },
		ProtoV6ProviderFactories: acctest.ProtoV6ProviderFactories,
		CheckDestroy:             testAccCheckVaultCredentialArchivedAndCleanup,
		Steps: []resource.TestStep{
			{
				Config: testAccVaultCredentialArchiveOnDestroyConfig,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttrSet("anthropic_vault_credential.bearer", "id"),
					resource.TestCheckResourceAttr("anthropic_vault_credential.bearer", "archive_on_destroy", "true"),
				),
			},
		},
	})
}
