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

# anthropic_service_accounts has no required input, so with a fully-known
# config it would be read at plan time -- and the dummy auth_token above would
# make that live call fail. depends_on on a resource that is never applied
# under `command = plan` defers the read past plan, so this fixture only
# validates the schema.
resource "anthropic_workspace" "seed" {
  name = "plan-only-seed"
}

data "anthropic_service_accounts" "test" {
  depends_on = [anthropic_workspace.seed]
}

output "seed_workspace_name" {
  value = anthropic_workspace.seed.name
}

# Referencing the data source in an output is enough to satisfy tflint's
# terraform_unused_declarations rule; its value stays unknown under
# `command = plan` since the read is deferred to apply (which never runs
# here), so this output isn't asserted on.
output "service_accounts" {
  value = data.anthropic_service_accounts.test.service_accounts
}
