terraform {
  required_version = "~> 1.0"
  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}

# List all organization members.
data "anthropic_organization_members" "all" {}

output "member_count" {
  value = length(data.anthropic_organization_members.all.members)
}

output "member_emails" {
  value = [for m in data.anthropic_organization_members.all.members : m.email]
}

# Filter by email to resolve a single user.
data "anthropic_organization_members" "by_email" {
  email = "user@emaildomain.com"
}

output "matched_by_email" {
  value = [for m in data.anthropic_organization_members.by_email.members : m.id]
}

output "admins" {
  value = [
    for m in data.anthropic_organization_members.all.members : m.id
    if m.role == "admin"
  ]
}
