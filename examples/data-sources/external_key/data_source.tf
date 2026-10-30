# Look up an external key by ID (`ekey_...`, or the KMS key ARN on Claude
# Platform on AWS).
data "anthropic_external_key" "example" {
  id = "ekey_01SDCCSbTxrXDpWc1phhtcfK"
}

output "external_key_provider" {
  description = "KMS provider of the key (`aws`, `gcp` or `azure`)."
  value       = data.anthropic_external_key.example.provider_config.type
}

output "external_key_attachment" {
  description = "Whether a workspace uses the key (`attached` or `unattached`)."
  value       = data.anthropic_external_key.example.attachment
}
