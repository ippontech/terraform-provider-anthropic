# Minimal vault
resource "anthropic_vault" "minimal" {
  display_name = "my-vault"
}

# Vault with metadata
resource "anthropic_vault" "with_metadata" {
  display_name = "vault-with-metadata"

  metadata = {
    team        = "platform"
    environment = "production"
  }
}

# Vault archived instead of deleted on terraform destroy
resource "anthropic_vault" "preserved" {
  display_name       = "preserved-vault"
  archive_on_destroy = true
}

output "vault_id" {
  description = "ID of the vault."
  value       = anthropic_vault.minimal.id
}

output "vault_created_at" {
  description = "Creation timestamp of the vault (RFC 3339)."
  value       = anthropic_vault.minimal.created_at
}
