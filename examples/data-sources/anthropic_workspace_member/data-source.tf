terraform {
  required_version = ">= 1.0"

  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}

variable "workspace_id" {
  type = string
}

variable "user_id" {
  type = string
}

data "anthropic_workspace_member" "example" {
  workspace_id = var.workspace_id
  user_id      = var.user_id
}

output "workspace_role" {
  value = data.anthropic_workspace_member.example.workspace_role
}
