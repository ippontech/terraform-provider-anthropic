data "anthropic_workspaces" "all" {}

output "workspaces_count" {
  value = length(data.anthropic_workspaces.all.workspaces)
}
