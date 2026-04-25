terraform {
  required_version = "~> 1.0"

  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}

# Minimal workspace — data_residency defaults applied by the API.
resource "anthropic_workspace" "example" {
  name = "Example Workspace"
}

# Workspace with explicit data residency.
resource "anthropic_workspace" "example_with_residency" {
  name = "Example Workspace with Residency"

  data_residency = {
    workspace_geo          = "us"
    default_inference_geo  = "global"
    allowed_inference_geos = ["unrestricted"]
  }
}

output "workspace_id" {
  value = anthropic_workspace.example.id
}

output "workspace_display_color" {
  value = anthropic_workspace.example.display_color
}
