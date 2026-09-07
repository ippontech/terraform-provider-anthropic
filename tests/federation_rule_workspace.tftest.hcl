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

run "federation_rule_workspace_plan_validates_schema" {
  command = plan

  module {
    source = "../examples/resources/federation_rule_workspace"
  }

  assert {
    condition     = anthropic_federation_rule_workspace.gha_deploy_staging.workspace_id == var.staging_workspace_id
    error_message = "Expected workspace_id to follow var.staging_workspace_id."
  }

  assert {
    condition     = anthropic_federation_rule.gha_deploy.workspace_id == "wrkspc_01HMrPGQfWoZ5LnhFhxuvNsm"
    error_message = "Expected the rule's own binding to stay on wrkspc_01HMrPGQfWoZ5LnhFhxuvNsm."
  }
}
