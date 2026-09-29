resource "anthropic_memory_store" "example" {
  name        = "example-memory-store"
  description = "Example memory store for the data source."
}

data "anthropic_memory_store" "example" {
  id = anthropic_memory_store.example.id
}

output "memory_store" {
  description = "Information about the retrieved memory store."
  value       = data.anthropic_memory_store.example
}
