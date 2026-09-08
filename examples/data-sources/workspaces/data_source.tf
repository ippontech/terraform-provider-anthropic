data "anthropic_workspaces" "all" {}

output "workspaces_count" {
  description = "Number of workspaces in the organization."
  value       = length(data.anthropic_workspaces.all.workspaces)
}
