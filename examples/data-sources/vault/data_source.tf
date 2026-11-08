# Look up a vault by ID, for example one created in another configuration or
# in the Console. The vault is created here so the example is self-contained.
resource "anthropic_vault" "example" {
  display_name = "example-vault"

  metadata = {
    team = "platform"
  }
}

data "anthropic_vault" "example" {
  id = anthropic_vault.example.id
}

output "vault_display_name" {
  description = "Display name of the vault."
  value       = data.anthropic_vault.example.display_name
}

output "vault_metadata" {
  description = "Metadata attached to the vault."
  value       = data.anthropic_vault.example.metadata
}

output "vault_archived" {
  description = "Whether the vault is archived."
  value       = data.anthropic_vault.example.archived_at != null
}
