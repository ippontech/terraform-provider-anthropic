test {
  parallel = true
}

# Admin API resource: no dedicated test organization exists yet (#58), so this
# test plans the public example with a dummy admin key. Configure only needs a
# non-empty credential and a create plan never calls the API (#233).
provider "anthropic" {
  admin_api_key = "dummy-admin-api-key"
}

run "organization_member_plan_validates_schema" {
  command = plan

  module {
    source = "../examples/resources/organization_member"
  }

  assert {
    condition     = anthropic_organization_member.example.id == "user_01ABCDEFGHIJKLMNOPQRSTUVWX"
    error_message = "Expected id to be 'user_01ABCDEFGHIJKLMNOPQRSTUVWX'."
  }

  assert {
    condition     = anthropic_organization_member.example.role == "developer"
    error_message = "Expected role to be 'developer'."
  }
}
