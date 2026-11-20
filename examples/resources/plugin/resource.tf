# Uploads the plugin and its first version from the plugin's root directory
# (its manifest is at sales-toolkit/.claude-plugin/plugin.json). Publish later
# content with anthropic_plugin_version.
resource "anthropic_plugin" "example" {
  source_dir    = "${path.module}/sales-toolkit"
  release_notes = "First release"

  # Once the plugin exists, to choose the version members are served (roll
  # back or promote), set its ID. This pins the plugin, which cannot be undone.
  # served_version_id = "pluginver_01Km7tL4pR9xF5sU2zV3jP6q"
}

output "plugin_id" {
  description = "ID of the plugin."
  value       = anthropic_plugin.example.id
}

output "plugin_name" {
  description = "Name of the plugin, from its manifest."
  value       = anthropic_plugin.example.name
}

output "served_version_id" {
  description = "Version members are served."
  value       = anthropic_plugin.example.served_version_id
}
