# Live vaults of the workspace.
data "anthropic_vaults" "live" {}

# Archived vaults included.
data "anthropic_vaults" "all" {
  include_archived = true
}

output "vault_ids" {
  description = "IDs of the live vaults, newest first."
  value       = [for v in data.anthropic_vaults.live.vaults : v.id]
}

output "archived_vault_count" {
  description = "Number of archived vaults in the workspace."
  value       = length([for v in data.anthropic_vaults.all.vaults : v if v.archived_at != null])
}
