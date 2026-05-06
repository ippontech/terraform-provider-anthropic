test {
  parallel = true
}

variables {
  workspace_id = "wrkspc_01HMrPGQfWoZ5LnhFhxuvNsm"
}

run "workspace_members_data_source_lists_members" {
  parallel = true

  module {
    source = "./examples/data-sources/anthropic_workspace_members"
  }

  variables {
    workspace_id = var.workspace_id
  }

  assert {
    condition     = output.members_count > 0
    error_message = "Expected at least one member to be returned."
  }
}
