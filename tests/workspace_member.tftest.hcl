test {
  parallel = true
}

# Admin API resource: no dedicated test organization exists yet (#58), so this
# test plans the public example with a dummy admin key. Configure only needs a
# non-empty credential and a create plan never calls the API (#233).
provider "anthropic" {
  admin_api_key = "dummy-admin-api-key"
}

run "workspace_member_plan_validates_schema" {
  command = plan

  module {
    source = "../examples/resources/workspace_member"
  }

  assert {
    condition     = anthropic_workspace_member.example.user_id == "user_01ABCDEFGHIJKLMNOPQRSTUVWX"
    error_message = "Expected user_id to be 'user_01ABCDEFGHIJKLMNOPQRSTUVWX'."
  }

  assert {
    condition     = anthropic_workspace_member.example.workspace_role == "workspace_developer"
    error_message = "Expected workspace_role to be 'workspace_developer'."
  }
}
