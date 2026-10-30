data "anthropic_rbac_groups" "all" {}

output "rbac_group_ids_by_name" {
  description = "IDs of the RBAC groups, keyed by name (names are not unique, so each name maps to a list)."
  value       = { for g in data.anthropic_rbac_groups.all.rbac_groups : g.name => g.id... }
}

output "scim_rbac_group_ids" {
  description = "IDs of the groups provisioned by the identity provider, which are read-only through the API."
  value       = [for g in data.anthropic_rbac_groups.all.rbac_groups : g.id if g.source_type == "scim"]
}
