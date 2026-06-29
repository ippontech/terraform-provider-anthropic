terraform {
  required_version = "~> 1.0"
  required_providers {
    anthropic = {
      source  = "registry.terraform.io/ippontech/anthropic"
      version = "~> 1.0"
    }
  }
}

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
  value = anthropic_vault.minimal.id
}

output "vault_created_at" {
  value = anthropic_vault.minimal.created_at
}
