data "anthropic_plugin_marketplace" "example" {
  id = "marketplace_01VbNcMxZaSdFgHjKlQwErTy"
}

output "marketplace_name" {
  description = "Name of the marketplace."
  value       = data.anthropic_plugin_marketplace.example.name
}

output "marketplace_default_installation_preference" {
  description = "Installation setting of the marketplace's plugins that have none of their own."
  value       = data.anthropic_plugin_marketplace.example.default_installation_preference
}
