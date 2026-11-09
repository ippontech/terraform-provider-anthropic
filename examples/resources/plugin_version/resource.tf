resource "anthropic_plugin" "example" {
  source_dir = "${path.module}/sales-toolkit"
}

# Publishes the directory's content as a version of the plugin, and publishes
# it again whenever a file in it changes: the content hash is recomputed at
# every plan. (Creating the plugin above uploaded its first version, so the
# first apply stores two versions of the same files.)
resource "anthropic_plugin_version" "example" {
  plugin_id     = anthropic_plugin.example.id
  source_dir    = "${path.module}/sales-toolkit"
  release_notes = "Adds the account research skill."
}

output "plugin_version_id" {
  description = "ID of the version."
  value       = anthropic_plugin_version.example.id
}

output "content_scan_status" {
  description = "Status of the version's content scan (processing right after the upload, null when scanning is off)."
  value       = try(anthropic_plugin_version.example.content_scan.status, null)
}
