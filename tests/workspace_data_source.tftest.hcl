test {
  parallel = true
}

run "workspace_data_source_reads_workspace" {
  command  = plan
  parallel = true
  module { source = "./examples/data-sources/workspace" }

  assert {
    condition     = output.workspace_name != ""
    error_message = "Expected workspace_name to be non-empty."
  }
  assert {
    condition     = output.created_at != ""
    error_message = "Expected created_at to be non-empty."
  }
  assert {
    condition     = output.display_color != ""
    error_message = "Expected display_color to be non-empty."
  }
}
