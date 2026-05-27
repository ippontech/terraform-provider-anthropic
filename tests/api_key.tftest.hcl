test {
  parallel = true
}

run "api_key_plan_validates_schema" {
  command = plan

  module {
    source = "./tests/fixtures/api_key_plan"
  }

  assert {
    condition     = output.api_key_name == "plan-test-key"
    error_message = "Expected name to be 'plan-test-key'."
  }

  assert {
    condition     = output.api_key_status == "active"
    error_message = "Expected status to be 'active'."
  }
}
