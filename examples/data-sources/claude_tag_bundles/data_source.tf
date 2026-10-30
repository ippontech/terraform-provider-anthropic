data "anthropic_claude_tag_bundles" "all" {}

output "bundle_ids_by_name" {
  description = "IDs of the organization's Access bundles, by name."
  value       = { for bundle in data.anthropic_claude_tag_bundles.all.bundles : bundle.display_name => bundle.id }
}
