data "anthropic_memory" "example" {
  memory_store_id = "memstore_01AAAAAAAAAAAAAAAAAAAAAA"
  id              = "mem_01AAAAAAAAAAAAAAAAAAAAAA"
}

output "memory" {
  description = "Information about the retrieved memory."
  value       = data.anthropic_memory.example
}
