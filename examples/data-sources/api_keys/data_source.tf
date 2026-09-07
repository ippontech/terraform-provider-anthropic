# List all active API keys for a workspace.
# The workspace_id references a resource (unknown during plan),
# which defers the data source read until apply.
resource "anthropic_workspace" "example" {
  name = "example-workspace"
}

data "anthropic_api_keys" "workspace_active" {
  status       = "active"
  workspace_id = anthropic_workspace.example.id
}

output "active_key_count" {
  description = "Number of active API keys in the workspace."
  value       = length(data.anthropic_api_keys.workspace_active.api_keys)
}

output "active_key_names" {
  description = "Names of the active API keys in the workspace."
  value       = [for k in data.anthropic_api_keys.workspace_active.api_keys : k.name]
}
