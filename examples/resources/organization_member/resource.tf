# Organization membership is created by inviting a user and having them
# accept the invite — it cannot be created via Terraform. Import an existing
# member first:
#   terraform import anthropic_organization_member.example <user_id>
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
