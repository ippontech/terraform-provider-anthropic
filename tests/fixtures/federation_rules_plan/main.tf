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
  auth_token    = "dummy-auth-token"
  admin_api_key = "dummy-admin-api-key" # only for the seed resource's Configure guard
}

# Never applied: exists only so depends_on defers the data source read past
# plan. anthropic_federation_rules requires a real org:admin OAuth bearer
# token, which the dummy auth_token above is not, so the read must never
# actually run under `command = plan`.
resource "anthropic_workspace" "seed" {
  name = "plan-only-seed"
}

data "anthropic_federation_rules" "test" {
  depends_on       = [anthropic_workspace.seed]
  issuer_id        = "fdis_01placeholder"
  include_archived = true
}

output "federation_rules_issuer_id" {
  value = data.anthropic_federation_rules.test.issuer_id
}

output "federation_rules_include_archived" {
  value = data.anthropic_federation_rules.test.include_archived
}
