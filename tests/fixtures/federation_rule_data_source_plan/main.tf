terraform {
  required_version = "~> 1.0"
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
# plan. anthropic_federation_rule's config is otherwise fully known, so under
# `command = plan` Terraform would read it immediately (before apply) using
# the dummy auth_token above, which would fail against the live API. Chaining
# through an unapplied resource keeps the data source's inputs unknown at
# plan time, deferring the read to an apply that this test never runs.
resource "anthropic_workspace" "seed" {
  name = "plan-only-seed"
}

data "anthropic_federation_rule" "test" {
  depends_on = [anthropic_workspace.seed]
  id         = "fdrl_placeholder"
}

output "federation_rule_id" {
  value = data.anthropic_federation_rule.test.id
}
