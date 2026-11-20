data "anthropic_plugin_versions" "example" {
  plugin_id = "plugin_01Hq3vX8kZcN2mB7pR4tY9wL"
}

output "plugin_version_ids" {
  description = "IDs of the plugin's versions, newest first."
  value       = [for version in data.anthropic_plugin_versions.example.versions : version.id]
}
