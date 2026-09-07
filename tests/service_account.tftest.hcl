test {
  parallel = true
}

# WIF resource: the endpoint only accepts an org:admin OAuth bearer token and
# CI has no durable one (#137), so this test plans the public example with a
# dummy token. Configure only needs a non-empty credential and a create plan
# never calls the API (#233).
provider "anthropic" {
  auth_token = "dummy-auth-token"
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
