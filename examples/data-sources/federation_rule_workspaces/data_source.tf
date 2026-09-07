data "anthropic_federation_rule_workspaces" "example" {
  federation_rule_id = "fdrl_01ABCDEFabcdef0123456789XY"
}

output "federation_rule_workspaces_count" {
  description = "Number of workspaces where the federation rule is enabled."
  value       = length(data.anthropic_federation_rule_workspaces.example.workspaces)
}

output "federation_rule_workspace_ids" {
  description = "IDs of the workspaces where the federation rule is enabled."
  value       = [for w in data.anthropic_federation_rule_workspaces.example.workspaces : w.workspace_id]
}
