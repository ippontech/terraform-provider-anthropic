test {
  parallel = true
}

run "memories_data_source_lists_memories" {
  module {
    source = "../examples/data-sources/memories"
  }

  assert {
    condition     = length(data.anthropic_memories.all.memories) >= 0
    error_message = "Expected memories to be a list (possibly empty)."
  }

  assert {
    condition     = length(data.anthropic_memories.all.prefixes) >= 0
    error_message = "Expected prefixes to be a list (possibly empty)."
  }
}
