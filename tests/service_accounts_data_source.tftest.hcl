test {
  parallel = true
}

run "service_accounts_data_source_validates_schema" {
  command = plan

  module {
    source = "./tests/fixtures/service_accounts_plan"
  }

  assert {
    condition     = output.seed_workspace_name == "plan-only-seed"
    error_message = "Expected seed_workspace_name to be 'plan-only-seed'."
  }
}
