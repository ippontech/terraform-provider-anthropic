test {
  parallel = true
}

run "memory_store_data_source_fetches_memory_store" {
  module {
    source = "../examples/data-sources/memory_store"
  }

  assert {
    condition     = data.anthropic_memory_store.example.id == anthropic_memory_store.example.id
    error_message = "Expected the data source id to match the resource id."
  }

  assert {
    condition     = data.anthropic_memory_store.example.name == "example-memory-store"
    error_message = "Expected name to be \"example-memory-store\"."
  }

  assert {
    condition     = data.anthropic_memory_store.example.archived_at == null
    error_message = "Expected archived_at to be null for a freshly created memory store."
  }
}
