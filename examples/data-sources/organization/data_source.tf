terraform {
  required_version = "~> 1.0"
  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}

# Read the organization tied to the configured admin API key
data "anthropic_organization" "current" {}

output "organization_id" {
  value = data.anthropic_organization.current.id
}

output "organization_name" {
  value = data.anthropic_organization.current.name
}

output "organization_type" {
  value = data.anthropic_organization.current.type
}
