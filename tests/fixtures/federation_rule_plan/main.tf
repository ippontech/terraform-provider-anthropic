terraform {
  required_version = ">= 1.6"

  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}

provider "anthropic" {
  auth_token = "dummy-auth-token"
}

resource "anthropic_federation_rule" "example" {
  name        = "plan-test-rule"
  description = "plan-only schema validation"
  issuer_id   = "fdis_01PLANTEST"

  match = {
    subject_prefix = "repo:my-org/my-repo:*"
  }

  target = {
    service_account_id = "svac_01PLANTEST"
  }

  oauth_scope            = "workspace:developer"
  workspace_id           = "wrkspc_01PLANTEST"
  token_lifetime_seconds = 1800
}

output "federation_rule_name" {
  value = anthropic_federation_rule.example.name
}

output "federation_rule_oauth_scope" {
  value = anthropic_federation_rule.example.oauth_scope
}

output "federation_rule_token_lifetime_seconds" {
  value = anthropic_federation_rule.example.token_lifetime_seconds
}
