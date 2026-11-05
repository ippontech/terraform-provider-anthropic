data "anthropic_rbac_group" "example" {
  id = "rbac_group_01UvWxYzAbCdEfGhIjKlMn"
}

output "rbac_group_name" {
  description = "Name of the RBAC group."
  value       = data.anthropic_rbac_group.example.name
}

output "rbac_group_source_type" {
  description = "How the group was created (`direct` or `scim`)."
  value       = data.anthropic_rbac_group.example.source_type
}

output "rbac_group_role_ids" {
  description = "IDs of the custom roles attached to the group."
  value       = data.anthropic_rbac_group.example.role_ids
}
