test {
  parallel = true
}

run "memory_stores_data_source_lists_memory_stores" {
  module {
    source = "../examples/data-sources/memory_stores"
  }

  assert {
    condition     = length(data.anthropic_memory_stores.all.memory_stores) >= 1
    error_message = "Expected at least one memory store to be listed."
  }
}
