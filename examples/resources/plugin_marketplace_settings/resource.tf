# Every plugin in this marketplace without a setting of its own, including the
# plugins added later, can be installed on request.
resource "anthropic_plugin_marketplace_settings" "engineering_tools" {
  marketplace_id                  = "marketplace_01VbNcMxZaSdFgHjKlQwErTy"
  default_installation_preference = "available"
}

output "marketplace_name" {
  description = "Name of the marketplace."
  value       = anthropic_plugin_marketplace_settings.engineering_tools.name
}
