variable "include_archived" {
  description = "Optional filter to include archived memory stores. Defaults to false."
  type        = bool
  default     = null
}

resource "anthropic_memory_store" "example" {
  name        = "example-memory-store-list"
  description = "Example memory store for the list data source."
}

data "anthropic_memory_stores" "all" {
  include_archived = var.include_archived
  depends_on       = [anthropic_memory_store.example]
}

output "memory_stores" {
  description = "List of memory stores."
  value       = data.anthropic_memory_stores.all.memory_stores
}
