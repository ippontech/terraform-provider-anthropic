resource "anthropic_workspace" "example" {
  name = "Example Workspace"

  data_residency = {
    workspace_geo          = "us"
    default_inference_geo  = "global"
    allowed_inference_geos = ["unrestricted"]
  }

  # Keys must not begin with "anthropic". To remove every tag, set `tags = {}`.
  tags = {
    env  = "example"
    team = "platform"
  }

  # Write-once CMEK key (requires CMEK enabled for the organization and an
  # existing key configuration); changing it forces a new workspace.
  # external_key_id = "ekey_..."
}

output "workspace_id" {
  description = "ID of the workspace."
  value       = anthropic_workspace.example.id
}

output "workspace_display_color" {
  description = "Display color assigned to the workspace in the Console."
  value       = anthropic_workspace.example.display_color
}

output "workspace_compartment_id" {
  description = "Encryption compartment ID of the workspace (reference it in the KMS key policy for CMEK on AWS)."
  value       = anthropic_workspace.example.compartment_id
}
