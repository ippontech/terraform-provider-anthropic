test {
  parallel = true
}

# Admin API resource: no dedicated test organization exists yet (#58), so this
# test plans the public example with a dummy admin key. Configure only needs a
# non-empty credential and a create plan never calls the API (#233).
provider "anthropic" {
  admin_api_key = "dummy-admin-api-key"
}

run "api_key_plan_validates_schema" {
  command = plan

  module {
    source = "./examples/resources/api_key"
  }

  assert {
    condition     = anthropic_api_key.example.name == "My Managed Key"
    error_message = "Expected name to be 'My Managed Key'."
  }

  assert {
    condition     = anthropic_api_key.example.status == "active"
    error_message = "Expected status to be 'active'."
  }
}
