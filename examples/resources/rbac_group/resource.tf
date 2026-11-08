resource "anthropic_rbac_group" "example" {
  name = "Engineering"
}

output "rbac_group_id" {
  description = "ID of the RBAC group."
  value       = anthropic_rbac_group.example.id
}

output "rbac_group_role_ids" {
  description = "IDs of the custom roles attached to the group in the claude.ai admin settings (read-only)."
  value       = anthropic_rbac_group.example.role_ids
}
