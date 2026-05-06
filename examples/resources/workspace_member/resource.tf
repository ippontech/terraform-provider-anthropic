terraform {
  required_version = "~> 1.0"

  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}

resource "anthropic_workspace" "example" {
  name = "Example Workspace"
}

resource "anthropic_workspace_member" "example" {
  workspace_id   = anthropic_workspace.example.id
  user_id        = "user_01ABCDEFGHIJKLMNOPQRSTUVWX"
  workspace_role = "workspace_developer"
}
