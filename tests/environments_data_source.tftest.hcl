test {
  parallel = true
}

run "environments_data_source_lists_environments" {
  parallel = true
  module { source = "./examples/data-sources/environments" }

  assert {
    condition     = output.environments_count >= 0
    error_message = "Expected environments_count to be a non-negative number."
  }
}
