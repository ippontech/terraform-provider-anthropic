terraform {
  required_version = "~> 1.0"

  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}

# When you already know the federation rule ID, look it up directly:
#
#   data "anthropic_federation_rule_workspaces" "example" {
#     federation_rule_id = "fdrl_01ABC"
#   }
#
# This example instead resolves a real rule ID from the rules list, so it can
# run end-to-end once the anthropic_federation_rules data source is available.
data "anthropic_federation_rules" "all" {}

data "anthropic_federation_rule_workspaces" "example" {
  federation_rule_id = data.anthropic_federation_rules.all.rules[0].id
}

output "federation_rule_workspaces_count" {
  description = "Number of workspaces where the federation rule is enabled."
  value       = length(data.anthropic_federation_rule_workspaces.example.workspaces)
}

output "federation_rule_workspace_ids" {
  description = "IDs of the workspaces where the federation rule is enabled."
  value       = [for w in data.anthropic_federation_rule_workspaces.example.workspaces : w.workspace_id]
}
