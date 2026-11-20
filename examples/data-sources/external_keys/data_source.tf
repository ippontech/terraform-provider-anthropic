# List every external key registered with the organization.
data "anthropic_external_keys" "all" {}

output "external_key_ids" {
  description = "IDs of the external keys, newest first."
  value       = [for k in data.anthropic_external_keys.all.external_keys : k.id]
}

output "unattached_external_key_ids" {
  description = "IDs of the keys no workspace uses yet (the only ones that can still change or be deleted)."
  value       = [for k in data.anthropic_external_keys.all.external_keys : k.id if k.attachment == "unattached"]
}
