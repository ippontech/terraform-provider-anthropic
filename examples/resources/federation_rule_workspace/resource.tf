terraform {
  required_version = "~> 1.0"

  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}

variable "staging_workspace_id" {
  description = "ID of the additional workspace to enable the rule for."
  type        = string
  default     = "wrkspc_01HMrPGQfWoZ5LnhFhxuvNsm"
}

# Trust GitHub Actions' OIDC issuer so its workflow tokens can be exchanged for
# short-lived Anthropic access tokens (no long-lived API key stored in CI).
resource "anthropic_federation_issuer" "github_actions" {
  issuer_url = "https://token.actions.githubusercontent.com"
  name       = "github-actions"
}

# Identity minted tokens act as.
resource "anthropic_service_account" "gha_deploy" {
  name = "gha-deploy"
}

# Bind the issuer to the service account. The rule's own workspace_id already
# enables one workspace (production, below) at create time.
resource "anthropic_federation_rule" "gha_deploy" {
  name        = "gha-deploy"
  description = "GitHub Actions deploy workflow on main"
  issuer_id   = anthropic_federation_issuer.github_actions.id

  match = {
    subject_prefix = "repo:my-org/my-repo:ref:refs/heads/main"
  }

  target = {
    service_account_id = anthropic_service_account.gha_deploy.id
  }

  oauth_scope  = "workspace:developer"
  workspace_id = "wrkspc_01HMrPGQfWoZ5LnhFhxuvNsm"

  token_lifetime_seconds = 900
}

# Also enable the same rule for a second workspace, beyond the one it was
# created with above.
resource "anthropic_federation_rule_workspace" "gha_deploy_staging" {
  federation_rule_id = anthropic_federation_rule.gha_deploy.id
  workspace_id       = var.staging_workspace_id
}

output "federation_rule_workspace_id" {
  value = anthropic_federation_rule_workspace.gha_deploy_staging.id
}

output "federation_rule_workspace_name" {
  value = anthropic_federation_rule_workspace.gha_deploy_staging.workspace_name
}
