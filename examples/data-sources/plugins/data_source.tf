data "anthropic_plugins" "all" {}

# A security inventory: the plugins whose served version is not classified as
# contained (it declares an MCP server or a CLI, takes effect on members'
# machines, or is not classified yet), or whose content scan did not pass (a
# concern, or no completed scan). A plugin nobody classified is listed, not
# skipped.
output "plugins_to_review" {
  description = "IDs of the plugins to review."
  value = [
    for plugin in data.anthropic_plugins.all.plugins : plugin.id
    if plugin.reach != "contained" || try(plugin.content_scan.assessment, null) != "pass"
  ]
}
