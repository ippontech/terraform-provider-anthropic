# List all organization members.
data "anthropic_organization_members" "all" {}

output "member_count" {
  description = "Number of organization members."
  value       = length(data.anthropic_organization_members.all.members)
}

output "member_emails" {
  description = "Email addresses of all organization members."
  # Marked sensitive so addresses are redacted in plan/apply/test output (e.g. CI logs).
  value     = [for m in data.anthropic_organization_members.all.members : m.email]
  sensitive = true
}

# Filter by email to resolve a single user.
data "anthropic_organization_members" "by_email" {
  email = "user@emaildomain.com"
}

output "matched_by_email" {
  description = "IDs of the members matching the email filter."
  value       = [for m in data.anthropic_organization_members.by_email.members : m.id]
}

output "admins" {
  description = "IDs of the members holding the admin role."
  value = [
    for m in data.anthropic_organization_members.all.members : m.id
    if m.role == "admin"
  ]
}
