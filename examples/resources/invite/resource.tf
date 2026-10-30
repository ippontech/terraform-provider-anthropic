# Claude Console organization: invite a developer.
resource "anthropic_invite" "example" {
  email = "new-teammate@example.com"
  role  = "developer"
}

output "invite_id" {
  description = "ID of the invite."
  value       = anthropic_invite.example.id
}

output "invite_email" {
  description = "Email address the invite was sent to."
  value       = anthropic_invite.example.email
  sensitive   = true
}

output "invite_status" {
  description = "Status of the invite."
  value       = anthropic_invite.example.status
}

# Claude Enterprise organization: invite a managed member who joins an RBAC
# group on acceptance, and so gets the custom roles attached to the group.
# The scoped Admin API key needs write:rbac_groups in addition to write:members.
resource "anthropic_rbac_group" "engineering" {
  name = "Engineering"
}

resource "anthropic_invite" "engineer" {
  email          = "new-engineer@example.com"
  role           = "managed"
  rbac_group_ids = [anthropic_rbac_group.engineering.id]
}

output "engineer_invite_rbac_group_ids" {
  description = "RBAC groups the invited engineer joins on acceptance."
  value       = anthropic_invite.engineer.rbac_group_ids
}
