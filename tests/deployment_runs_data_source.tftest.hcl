test {
  parallel = true
}

# Filtering by a well-formed but non-existent deployment_id returns 200 with empty data, so
# the example applies for real without any deployment in the test workspace.
run "deployment_runs_data_source_lists_runs" {
  module { source = "../examples/data-sources/deployment_runs" }

  assert {
    condition     = output.run_count >= 0
    error_message = "Expected run_count to be a non-negative number."
  }
  assert {
    condition     = length(output.failed_run_errors) == 0
    error_message = "Expected no runs for the placeholder deployment ID."
  }
}
