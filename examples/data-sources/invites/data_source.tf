# List all organization invites.
data "anthropic_invites" "all" {}

output "invite_count" {
  description = "Number of invites."
  value       = length(data.anthropic_invites.all.invites)
}

output "invite_emails" {
  description = "Email addresses of all invites."
  # Marked sensitive so addresses are redacted in plan/apply/test output (e.g. CI logs).
  value     = [for i in data.anthropic_invites.all.invites : i.email]
  sensitive = true
}

# Filter by email to resolve a single invite.
data "anthropic_invites" "by_email" {
  email = "user@emaildomain.com"
}

output "matched_by_email" {
  description = "IDs of the invites matching the email filter."
  value       = [for i in data.anthropic_invites.by_email.invites : i.id]
}

# Filter by status to find still-pending invites.
data "anthropic_invites" "pending" {
  statuses = ["pending"]
}

output "pending_invite_ids" {
  description = "IDs of the invites still pending acceptance."
  value       = [for i in data.anthropic_invites.pending.invites : i.id]
}
