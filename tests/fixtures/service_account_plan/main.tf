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
# plan. A data source whose config is fully known is read at plan time, and
# with the dummy auth_token above that live call would fail; deferring the
# read past plan means it never runs under `command = plan`.
resource "anthropic_workspace" "seed" {
  name = "plan-only-seed"
}

data "anthropic_service_account" "test" {
  depends_on = [anthropic_workspace.seed]

  id = "svac_01PLACEHOLDER0000000000"
}

output "service_account_id" {
  value = data.anthropic_service_account.test.id
}
