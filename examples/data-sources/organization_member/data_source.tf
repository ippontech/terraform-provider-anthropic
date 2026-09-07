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
  description = "Email address of the member."
  # Marked sensitive so the address is redacted in plan/apply/test output (e.g. CI logs).
  value     = data.anthropic_organization_member.example.email
  sensitive = true
}

output "member_role" {
  description = "Organization role of the member."
  value       = data.anthropic_organization_member.example.role
}
