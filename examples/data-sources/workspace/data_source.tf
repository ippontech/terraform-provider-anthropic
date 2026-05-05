terraform {
  required_version = "~> 1.0"
  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}

# Create a workspace to read with the data source
resource "anthropic_workspace" "created" {
  name = "workspace-data-source-example"
}

# Look up the created workspace by ID
data "anthropic_workspace" "example" {
  id = anthropic_workspace.created.id
}

output "workspace_name" {
  value = data.anthropic_workspace.example.name
}

output "created_at" {
  value = data.anthropic_workspace.example.created_at
}

output "display_color" {
  value = data.anthropic_workspace.example.display_color
}
