# Minimal memory store
resource "anthropic_memory_store" "minimal" {
  name        = "my-memory-store"
  description = "Notes accumulated by the agent across sessions."
}

# Memory store with metadata
resource "anthropic_memory_store" "with_metadata" {
  name        = "memory-store-with-metadata"
  description = "Per-customer memory scoped to the support agent."

  metadata = {
    team        = "support"
    environment = "production"
  }
}

# Memory store archived instead of deleted on terraform destroy
resource "anthropic_memory_store" "preserved" {
  name               = "preserved-memory-store"
  description        = "Retained for audit purposes after the agent is decommissioned."
  archive_on_destroy = true
}

output "memory_store_id" {
  description = "ID of the memory store."
  value       = anthropic_memory_store.minimal.id
}

output "memory_store_created_at" {
  description = "Creation timestamp of the memory store (RFC 3339)."
  value       = anthropic_memory_store.minimal.created_at
}
