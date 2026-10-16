# The user must already be a member of the organization. Terraform adopts the
# existing member by ID and updates the role if it differs from this config.
resource "anthropic_organization_member" "example" {
  id   = "user_01ABCDEFGHIJKLMNOPQRSTUVWX"
  role = "developer"
}

output "organization_member_id" {
  description = "ID of the managed organization member."
  value       = anthropic_organization_member.example.id
}

output "organization_member_email" {
  description = "Email address of the member."
  # Marked sensitive so the address is redacted in plan/apply/test output (e.g. CI logs).
  value     = anthropic_organization_member.example.email
  sensitive = true
}
