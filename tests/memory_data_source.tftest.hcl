test {
  parallel = true
}

mock_provider "anthropic" {}

run "memory_data_source_plan" {
  module {
    source = "../examples/data-sources/memory"
  }

  assert {
    condition     = data.anthropic_memory.example.memory_store_id == "memstore_01AAAAAAAAAAAAAAAAAAAAAA"
    error_message = "Expected memory_store_id to echo the configured value."
  }

  assert {
    condition     = data.anthropic_memory.example.id == "mem_01AAAAAAAAAAAAAAAAAAAAAA"
    error_message = "Expected id to echo the configured value."
  }
}
