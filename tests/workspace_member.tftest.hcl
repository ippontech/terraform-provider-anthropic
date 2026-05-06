test {
  parallel = true
}

run "workspace_member_plan_validates_schema" {
  command = plan

  module {
    source = "./tests/fixtures/workspace_member_plan"
  }

  assert {
    condition     = output.workspace_member_workspace_id == "ws_01abc123"
    error_message = "Expected workspace_id to be 'ws_01abc123'."
  }

  assert {
    condition     = output.workspace_member_user_id == "user_01xyz789"
    error_message = "Expected user_id to be 'user_01xyz789'."
  }

  assert {
    condition     = output.workspace_member_role == "workspace_user"
    error_message = "Expected workspace_role to be 'workspace_user'."
  }
}
