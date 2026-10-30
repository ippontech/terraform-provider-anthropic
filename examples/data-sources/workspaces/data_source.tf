data "anthropic_workspaces" "all" {}

output "workspaces_count" {
  description = "Number of workspaces in the organization."
  value       = length(data.anthropic_workspaces.all.workspaces)
}

# The Default Workspace is listed only on request. The Admin API reports its
# ID in the scope.workspace_id of the API keys bound to it (their workspace_id
# is null).
data "anthropic_workspaces" "with_default" {
  include_default = true
}

# The Default Workspace is the one workspace that only the second list holds.
# Its name cannot tell it apart: another workspace may be named "Default" too.
output "default_workspace_id" {
  description = "ID of the organization's Default Workspace."
  value = one([
    for ws in data.anthropic_workspaces.with_default.workspaces : ws.id
    if !contains(data.anthropic_workspaces.all.workspaces[*].id, ws.id)
  ])
}
