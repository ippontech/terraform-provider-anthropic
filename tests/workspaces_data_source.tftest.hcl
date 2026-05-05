test {
  parallel = true
}

run "workspaces_data_source_lists_workspaces" {
  parallel = true
  module { source = "./examples/data-sources/workspaces" }

  assert {
    condition     = output.workspaces_count > 0
    error_message = "Expected at least one workspace to be returned."
  }
}
