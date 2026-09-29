# When you already know the invite ID, look it up directly:
#
#   data "anthropic_invite" "example" {
#     id = "invite_015gWxCN9Hfg2QhZwTK7"
#   }
#
# This example instead resolves a real invite ID from the invites list, so it
# can run end-to-end, then fetches that invite's full details by ID.
data "anthropic_invites" "all" {}

data "anthropic_invite" "example" {
  id = data.anthropic_invites.all.invites[0].id
}

output "invite_email" {
  description = "Email address the invite was sent to."
  # Marked sensitive so the address is redacted in plan/apply/test output (e.g. CI logs).
  value     = data.anthropic_invite.example.email
  sensitive = true
}

output "invite_status" {
  description = "Status of the invite."
  value       = data.anthropic_invite.example.status
}
