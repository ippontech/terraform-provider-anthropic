terraform {
  required_version = "~> 1.0"
  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}

# The vault that will hold all three credentials below.
resource "anthropic_vault" "example" {
  display_name = "Example vault"
}

# 1. Static bearer token credential
#    Required: type = "static_bearer", token (write-only), mcp_server_url
resource "anthropic_vault_credential" "bearer" {
  vault_id       = anthropic_vault.example.id
  type           = "static_bearer"
  display_name   = "My MCP bearer token"
  mcp_server_url = "https://mcp.example.com"

  # Write-only: never stored in state. Increment token_wo_version to rotate.
  token            = "my-secret-bearer-token"
  token_wo_version = 1

  metadata = {
    environment = "production"
    team        = "backend"
  }
}

# 2. MCP OAuth credential with a refresh block (token_endpoint_auth = client_secret_post)
#    Required: type = "mcp_oauth", access_token (write-only), mcp_server_url
resource "anthropic_vault_credential" "oauth" {
  vault_id       = anthropic_vault.example.id
  type           = "mcp_oauth"
  display_name   = "My MCP OAuth token"
  mcp_server_url = "https://mcp.example.com/oauth"

  # Write-only: never stored in state.
  access_token     = "my-access-token"
  expires_at       = "2026-12-31T23:59:59Z"
  token_wo_version = 1

  refresh = {
    client_id      = "my-client-id"
    token_endpoint = "https://auth.example.com/oauth/token"

    # Write-only: never stored in state.
    refresh_token = "my-refresh-token"

    token_endpoint_auth = {
      type = "client_secret_post"
      # Write-only: never stored in state.
      client_secret = "my-client-secret"
    }

    scope    = "openid profile"
    resource = "https://api.example.com"
  }
}

# 3. Environment variable credential with limited networking
#    Required: type = "environment_variable", secret_name, secret_value (write-only), networking
resource "anthropic_vault_credential" "env_var" {
  vault_id     = anthropic_vault.example.id
  type         = "environment_variable"
  display_name = "My API key as env var"
  secret_name  = "MY_API_KEY"

  # Write-only: never stored in state.
  secret_value     = "my-secret-api-key"
  token_wo_version = 1

  networking = {
    mode          = "limited"
    allowed_hosts = ["api.example.com", "*.internal.example.com"]
  }
}

output "vault_credential_bearer_id" {
  description = "ID of the static bearer credential."
  value       = anthropic_vault_credential.bearer.id
}

output "vault_credential_oauth_id" {
  description = "ID of the MCP OAuth credential."
  value       = anthropic_vault_credential.oauth.id
}

output "vault_credential_env_var_id" {
  description = "ID of the environment variable credential."
  value       = anthropic_vault_credential.env_var.id
}
