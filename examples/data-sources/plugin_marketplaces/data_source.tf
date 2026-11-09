data "anthropic_plugin_marketplaces" "organization" {
  owner_type = "organization"
}

output "marketplace_ids" {
  description = "IDs of the organization's marketplaces, by name."
  value       = { for marketplace in data.anthropic_plugin_marketplaces.organization.marketplaces : marketplace.name => marketplace.id }
}
