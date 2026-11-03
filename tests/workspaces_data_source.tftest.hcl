test {
  parallel = true
}

run "workspaces_data_source_lists_workspaces" {
  parallel = true
  module { source = "../examples/data-sources/workspaces" }

  assert {
    condition     = output.workspaces_count > 0
    error_message = "Expected at least one workspace to be returned."
  }

  # The example's output keeps the IDs that only the include_default list
  # holds, and one() fails on more than one. Comparing IDs rather than counts
  # keeps a workspace that changes between the two reads from shifting a total.
  assert {
    condition     = output.default_workspace_id != null
    error_message = "Expected include_default to add exactly the Default Workspace to the list."
  }
}
