terraform {
  required_version = "~> 1.0"

  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}

variable "workspace_id" {
  description = "The ID of the workspace whose members to list."
  type        = string
}

data "anthropic_workspace_members" "all" {
  workspace_id = var.workspace_id
}

output "members_count" {
  description = "Total number of members in the workspace."
  value       = length(data.anthropic_workspace_members.all.members)
}
