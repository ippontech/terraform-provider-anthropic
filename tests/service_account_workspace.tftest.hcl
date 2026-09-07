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

run "service_account_workspace_plan_validates_schema" {
  command = plan

  module {
    source = "./examples/resources/service_account_workspace"
  }

  assert {
    condition     = anthropic_service_account_workspace.ci.workspace_id == "wrkspc_01HMrPGQfWoZ5LnhFhxuvNsm"
    error_message = "Expected workspace_id to be 'wrkspc_01HMrPGQfWoZ5LnhFhxuvNsm'."
  }

  assert {
    condition     = anthropic_service_account_workspace.ci.workspace_role == "workspace_developer"
    error_message = "Expected workspace_role to be 'workspace_developer'."
  }
}
