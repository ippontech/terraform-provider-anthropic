resource "anthropic_memory_store" "example" {
  name        = "example-memory-store-for-memories"
  description = "Example memory store for the memories list data source."
}

data "anthropic_memories" "all" {
  memory_store_id = anthropic_memory_store.example.id
}

output "memories" {
  description = "List of memories in the store."
  value       = data.anthropic_memories.all.memories
}

output "memory_prefixes" {
  description = "Rolled-up path prefixes (only populated when depth = 1)."
  value       = data.anthropic_memories.all.prefixes
}
