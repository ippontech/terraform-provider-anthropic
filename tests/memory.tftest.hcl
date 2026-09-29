test {
  parallel = true
}

run "memory_resource_creates_memories" {
  module {
    source = "../examples/resources/memory"
  }

  assert {
    condition     = output.memory_id != ""
    error_message = "Expected memory_id to be non-empty."
  }

  assert {
    condition     = output.memory_content_sha256 != ""
    error_message = "Expected memory_content_sha256 to be non-empty."
  }

  assert {
    condition     = anthropic_memory.readme.path == "/README.md"
    error_message = "Expected the top-level memory's path to be /README.md."
  }

  assert {
    condition     = anthropic_memory.project_notes.path == "/projects/acme/notes.md"
    error_message = "Expected the nested memory's path to be /projects/acme/notes.md."
  }

  assert {
    condition     = anthropic_memory.readme.memory_version_id != ""
    error_message = "Expected memory_version_id to be non-empty."
  }
}
