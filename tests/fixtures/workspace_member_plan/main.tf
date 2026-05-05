terraform {
  required_version = ">= 1.6"

  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}

provider "anthropic" {
  admin_api_key = "dummy-admin-api-key"
}

resource "anthropic_workspace_member" "example" {
  workspace_id   = "ws_01abc123"
  user_id        = "user_01xyz789"
  workspace_role = "workspace_user"
}

output "workspace_member_workspace_id" {
  value = anthropic_workspace_member.example.workspace_id
}

output "workspace_member_user_id" {
  value = anthropic_workspace_member.example.user_id
}

output "workspace_member_role" {
  value = anthropic_workspace_member.example.workspace_role
}
