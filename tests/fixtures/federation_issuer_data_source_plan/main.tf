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
  admin_api_key = "dummy-admin-key" # only for the seed resource's Configure guard
}

# Never applied: exists only so depends_on defers the data source read past
# plan. A data source whose config is fully known at plan time is read during
# plan, and the dummy auth_token would make that live call fail; command =
# plan never reaches apply, so this resource is never actually created.
resource "anthropic_workspace" "seed" {
  name = "plan-only-seed"
}

data "anthropic_federation_issuer" "test" {
  depends_on = [anthropic_workspace.seed]

  id = "fdis_01PLANTEST"
}

output "federation_issuer_id" {
  value = data.anthropic_federation_issuer.test.id
}
