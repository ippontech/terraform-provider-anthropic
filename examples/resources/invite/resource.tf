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
