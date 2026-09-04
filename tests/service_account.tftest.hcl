test {
  parallel = true
}

run "service_account_plan" {
  command = plan

  module {
    source = "./examples/resources/service_account"
  }

  assert {
    condition     = output.service_account_name == "ci-runner"
    error_message = "Expected service_account_name to be 'ci-runner'."
  }

  assert {
    condition     = output.release_manager_role == "admin"
    error_message = "Expected release_manager_role to be 'admin'."
  }
}
