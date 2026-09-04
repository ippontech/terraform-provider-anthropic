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
#   data "anthropic_service_account" "example" {
#     id = "svac_01WCz1FkmYMm4gnmykNKUu3Q"
#   }
#
# This example instead resolves a real ID from the list data source, so it can
# run end-to-end, then fetches that service account's full details by ID.
data "anthropic_service_accounts" "all" {}

data "anthropic_service_account" "example" {
  id = data.anthropic_service_accounts.all.service_accounts[0].id
}

output "service_account_id" {
  value = data.anthropic_service_account.example.id
}

output "service_account_name" {
  value     = data.anthropic_service_account.example.name
  sensitive = true
}

output "service_account_organization_role" {
  value = data.anthropic_service_account.example.organization_role
}
