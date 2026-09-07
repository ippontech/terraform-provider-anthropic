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

run "federation_rule_plan_validates_schema" {
  command = plan

  module {
    source = "../examples/resources/federation_rule"
  }

  assert {
    condition     = anthropic_federation_rule.gha_deploy.name == "gha-deploy"
    error_message = "Expected name to be 'gha-deploy'."
  }

  assert {
    condition     = anthropic_federation_rule.gha_deploy.oauth_scope == "workspace:developer"
    error_message = "Expected oauth_scope to be 'workspace:developer'."
  }

  assert {
    condition     = anthropic_federation_rule.gha_deploy.token_lifetime_seconds == 900
    error_message = "Expected token_lifetime_seconds to be 900."
  }

  assert {
    condition     = anthropic_federation_rule.gha_deploy.match.subject_prefix == "repo:my-org/my-repo:ref:refs/heads/main"
    error_message = "Expected match.subject_prefix to pin the main branch."
  }
}
