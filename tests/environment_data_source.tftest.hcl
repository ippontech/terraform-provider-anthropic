test {
  parallel = true
}

run "environment_data_source_reads_environment" {
  parallel = true
  module { source = "./examples/data-sources/environment" }

  assert {
    condition     = output.environment_id != ""
    error_message = "Expected environment_id to be non-empty."
  }
  assert {
    condition     = output.environment_name != ""
    error_message = "Expected environment_name to be non-empty."
  }
  assert {
    condition     = output.environment_type == "environment"
    error_message = "Expected type == \"environment\"."
  }
}
