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
  auth_token = "dummy-auth-token"
}

resource "anthropic_service_account_workspace" "example" {
  service_account_id = "svac_01PLANTEST"
  workspace_id       = "wrkspc_01PLANTEST"
  workspace_role     = "workspace_developer"
}

output "service_account_workspace_service_account_id" {
  value = anthropic_service_account_workspace.example.service_account_id
}

output "service_account_workspace_workspace_id" {
  value = anthropic_service_account_workspace.example.workspace_id
}

output "service_account_workspace_workspace_role" {
  value = anthropic_service_account_workspace.example.workspace_role
}
