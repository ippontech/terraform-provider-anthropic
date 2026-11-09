data "anthropic_plugin" "example" {
  id = "plugin_01Hq3vX8kZcN2mB7pR4tY9wL"
}

output "plugin_name" {
  description = "Name of the plugin, from its manifest."
  value       = data.anthropic_plugin.example.name
}

output "plugin_reach" {
  description = "How far the served version reaches (remote, privileged or contained)."
  value       = data.anthropic_plugin.example.reach
}
