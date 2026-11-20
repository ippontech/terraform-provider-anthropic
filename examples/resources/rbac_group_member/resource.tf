resource "anthropic_rbac_group" "example" {
  name = "Engineering"
}

resource "anthropic_rbac_group_member" "example" {
  rbac_group_id = anthropic_rbac_group.example.id
  user_id       = "user_01AbCdEfGhIjKlMnOpQrSt"
}

output "rbac_group_member_id" {
  description = "Composite ID of the membership (`<rbac_group_id>:<user_id>`)."
  value       = anthropic_rbac_group_member.example.id
}

output "rbac_group_member_email" {
  description = "Email of the user added to the group."
  value       = anthropic_rbac_group_member.example.email
  sensitive   = true
}
