resource "anthropic_workspace" "example" {
  name = "Example Workspace"

  data_residency = {
    workspace_geo          = "us"
    default_inference_geo  = "global"
    allowed_inference_geos = ["unrestricted"]
  }
}

output "workspace_id" {
  description = "ID of the workspace."
  value       = anthropic_workspace.example.id
}

output "workspace_display_color" {
  description = "Display color assigned to the workspace in the Console."
  value       = anthropic_workspace.example.display_color
}
