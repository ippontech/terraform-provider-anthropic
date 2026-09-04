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

resource "anthropic_federation_rule_workspace" "example" {
  federation_rule_id = "fdrl_01PLANTEST"
  workspace_id       = "wrkspc_01PLANTEST"
}

output "federation_rule_workspace_federation_rule_id" {
  value = anthropic_federation_rule_workspace.example.federation_rule_id
}

output "federation_rule_workspace_workspace_id" {
  value = anthropic_federation_rule_workspace.example.workspace_id
}
