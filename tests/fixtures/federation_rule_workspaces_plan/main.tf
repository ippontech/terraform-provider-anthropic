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
# plan. anthropic_federation_rule_workspaces has no other resource on this
# branch to chain an unknown federation_rule_id off of (the sibling federation
# rule resource is still on its own PR), and a data source whose config is
# fully known is read at plan time — with the dummy auth_token that live call
# would fail. Depending on an uncreated resource keeps the read from ever
# running under `command = plan`, so federation_rule_id can stay a literal
# placeholder.
resource "anthropic_workspace" "seed" {
  name = "plan-only-seed"
}

data "anthropic_federation_rule_workspaces" "test" {
  depends_on         = [anthropic_workspace.seed]
  federation_rule_id = "fdrl_01ABCDEFGHIJKLMNOPQRSTUV"
}

output "federation_rule_id" {
  value = data.anthropic_federation_rule_workspaces.test.federation_rule_id
}
