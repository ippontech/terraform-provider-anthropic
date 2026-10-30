data "anthropic_plugins" "all" {}

# A security inventory: the plugins whose served version reaches outside the
# member's session (an MCP server or a CLI), or whose content scan raised a
# concern.
output "plugins_to_review" {
  description = "IDs of the plugins to review."
  value = [
    for plugin in data.anthropic_plugins.all.plugins : plugin.id
    if plugin.reach == "remote" || try(contains(["warn", "fail", "unknown"], plugin.content_scan.assessment), false)
  ]
}
