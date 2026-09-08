# Create a workspace to read with the data source
resource "anthropic_workspace" "created" {
  name = "workspace-data-source-example"
}

# Look up the created workspace by ID
data "anthropic_workspace" "example" {
  id = anthropic_workspace.created.id
}

output "workspace_name" {
  description = "Name of the workspace."
  value       = data.anthropic_workspace.example.name
}

output "created_at" {
  description = "Creation timestamp of the workspace (RFC 3339)."
  value       = data.anthropic_workspace.example.created_at
}

output "display_color" {
  description = "Display color of the workspace in the Console."
  value       = data.anthropic_workspace.example.display_color
}
