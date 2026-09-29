resource "anthropic_memory_store" "notes" {
  name        = "agent-notes"
  description = "Notes accumulated by the agent across sessions."
}

# A top-level memory
resource "anthropic_memory" "readme" {
  memory_store_id = anthropic_memory_store.notes.id
  path            = "/README.md"
  content         = "# Notes\n\nThis store holds cross-session context for the agent."
}

# A memory nested under a subdirectory-like path
resource "anthropic_memory" "project_notes" {
  memory_store_id = anthropic_memory_store.notes.id
  path            = "/projects/acme/notes.md"
  content         = "Acme project: prefers concise responses, uses Terraform for infra."
}

output "memory_id" {
  description = "ID of the top-level memory."
  value       = anthropic_memory.readme.id
}

output "memory_content_sha256" {
  description = "SHA-256 digest of the memory's content, used as the update precondition."
  value       = anthropic_memory.readme.content_sha256
}
