test {
  parallel = true
}

variables {
  workspace_id = ""
  user_id      = ""
}

run "workspace_member_data_source_reads_member" {
  parallel = true

  module {
    source = "./examples/data-sources/anthropic_workspace_member"
  }

  variables {
    workspace_id = var.workspace_id
    user_id      = var.user_id
  }

  assert {
    condition     = output.workspace_role != ""
    error_message = "Expected workspace_role to be non-empty."
  }
}
