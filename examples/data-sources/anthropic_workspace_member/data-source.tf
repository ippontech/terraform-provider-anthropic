terraform {
  required_version = ">= 1.0"

  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}

# Create a workspace to look up members from.
resource "anthropic_workspace" "created" {
  name = "workspace-member-data-source-example"
}

variable "user_id" {
  description = "The technical user ID of the workspace member to look up."
  type        = string
}

# Look up a member by workspace ID and user ID.
data "anthropic_workspace_member" "example" {
  workspace_id = anthropic_workspace.created.id
  user_id      = var.user_id
}

output "workspace_role" {
  value = data.anthropic_workspace_member.example.workspace_role
}
