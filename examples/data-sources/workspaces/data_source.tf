terraform {
  required_version = "~> 1.0"
  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}

data "anthropic_workspaces" "all" {}

output "workspaces_count" {
  value = length(data.anthropic_workspaces.all.workspaces)
}
