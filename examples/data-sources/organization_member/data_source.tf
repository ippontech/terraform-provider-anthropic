terraform {
  required_version = "~> 1.0"
  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}

# When you already know the user ID, look it up directly:
#
#   data "anthropic_organization_member" "example" {
#     id = "user_01WCz1FkmYMm4gnmykNKUu3Q"
#   }
#
# This example instead resolves a real user ID from the members list, so it can
# run end-to-end, then fetches that member's full details by ID.
data "anthropic_organization_members" "all" {}

data "anthropic_organization_member" "example" {
  id = data.anthropic_organization_members.all.members[0].id
}

output "member_email" {
  value = data.anthropic_organization_member.example.email
}

output "member_role" {
  value = data.anthropic_organization_member.example.role
}
