test {
  parallel = true
}

run "workspace_resource_creates_workspace" {
  parallel = true
  module {
    source = "./examples/resources/workspace"
  }

  assert {
    condition     = output.workspace_id != ""
    error_message = "Expected workspace_id to be non-empty."
  }

  assert {
    condition     = output.workspace_display_color != ""
    error_message = "Expected workspace_display_color to be non-empty."
  }
}
