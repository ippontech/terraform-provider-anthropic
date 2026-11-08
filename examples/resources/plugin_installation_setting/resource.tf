# Roll a plugin out to a pilot group: its members get it installed (and can
# remove it), while everyone else can install it on request.
resource "anthropic_plugin_installation_setting" "pilot" {
  plugin_id               = "plugin_01Hq3vX8kZcN2mB7pR4tY9wL"
  target                  = "rbac_group_01F3xQqQzXyWvUtSrQpOnMlK"
  installation_preference = "auto_install"
}

resource "anthropic_plugin_installation_setting" "organization" {
  plugin_id               = "plugin_01Hq3vX8kZcN2mB7pR4tY9wL"
  target                  = "organization"
  installation_preference = "available"
}

output "pilot_setting_id" {
  description = "ID of the pilot group's setting (<plugin_id>:<target>)."
  value       = anthropic_plugin_installation_setting.pilot.id
}
