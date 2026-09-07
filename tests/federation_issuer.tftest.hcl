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

run "federation_issuer_plan_validates_schema" {
  command = plan

  module {
    source = "../examples/resources/federation_issuer"
  }

  assert {
    condition     = anthropic_federation_issuer.github_actions.name == "github-actions"
    error_message = "Expected name to be 'github-actions'."
  }

  assert {
    condition     = anthropic_federation_issuer.github_actions.issuer_url == "https://token.actions.githubusercontent.com"
    error_message = "Expected issuer_url to be 'https://token.actions.githubusercontent.com'."
  }

  assert {
    condition     = anthropic_federation_issuer.github_actions.jwks.type == "discovery"
    error_message = "Expected jwks.type to be 'discovery'."
  }

  assert {
    condition     = anthropic_federation_issuer.github_actions.max_jwt_lifetime_seconds == 600
    error_message = "Expected max_jwt_lifetime_seconds to be 600."
  }
}
