test {
  parallel = true
}

run "memory_store_resource_creates_memory_store" {
  module {
    source = "../examples/resources/memory_store"
  }

  assert {
    condition     = output.memory_store_id != ""
    error_message = "Expected memory_store_id to be non-empty."
  }

  assert {
    condition     = output.memory_store_created_at != ""
    error_message = "Expected memory_store_created_at to be non-empty."
  }

  assert {
    condition     = anthropic_memory_store.minimal.description != ""
    error_message = "Expected description to be non-empty."
  }
}
