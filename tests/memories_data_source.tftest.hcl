test {
  parallel = true
}

run "memories_data_source_lists_memories" {
  module {
    source = "../examples/data-sources/memories"
  }

  assert {
    condition     = data.anthropic_memories.all.memory_store_id == anthropic_memory_store.example.id
    error_message = "Expected memory_store_id to echo the configured value."
  }

  assert {
    condition     = length(data.anthropic_memories.all.memories) == 0
    error_message = "Expected no memories in a freshly created memory store."
  }

  assert {
    condition     = length(data.anthropic_memories.all.prefixes) == 0
    error_message = "Expected no prefixes in a freshly created memory store."
  }
}
