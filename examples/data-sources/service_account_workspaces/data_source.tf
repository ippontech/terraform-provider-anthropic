terraform {
  required_version = "~> 1.0"

  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}

# When you already know the service account ID, look it up directly:
#
#   data "anthropic_service_account_workspaces" "example" {
#     service_account_id = "svac_01WCz1FkmYMm4gnmykNKUu3Q"
#   }
#
# This example instead resolves a real service account ID from the full list,
# so it can run end-to-end, then lists that service account's workspace
# memberships.
data "anthropic_service_accounts" "all" {}

data "anthropic_service_account_workspaces" "example" {
  service_account_id = data.anthropic_service_accounts.all.service_accounts[0].id
}

output "service_account_workspaces_count" {
  description = "Total number of workspace memberships (implicit and explicit) for the service account."
  value       = length(data.anthropic_service_account_workspaces.example.workspaces)
}
