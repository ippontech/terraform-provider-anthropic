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
# plan. anthropic_federation_issuers has no required input, so without this
# its Read would run at plan time (all inputs are already known) and fail
# against the dummy auth_token above — there is no durable org:admin OAuth
# token available in CI (see wave-3 WIF constraints, #137).
resource "anthropic_workspace" "seed" {
  name = "plan-only-seed"
}

data "anthropic_federation_issuers" "test" {
  depends_on = [anthropic_workspace.seed]
}

output "federation_issuers_count" {
  value = length(data.anthropic_federation_issuers.test.issuers)
}
