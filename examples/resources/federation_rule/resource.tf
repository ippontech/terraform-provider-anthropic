terraform {
  required_version = "~> 1.0"
  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
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

# Bind the issuer to the service account: only workflow tokens whose `sub`
# claim matches this repository's main-branch deploy job qualify.
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

output "federation_rule_id" {
  value = anthropic_federation_rule.gha_deploy.id
}

output "federation_rule_issuer_name" {
  value = anthropic_federation_rule.gha_deploy.issuer_name
}
