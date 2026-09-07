test {
  parallel = true
}

# This resource requires an org:admin OAuth bearer token, which endpoints like
# this one reject Admin API keys for outright, and no test organization or
# durable org:admin token exists in CI (see CLAUDE.md / acctest.PreCheckOAuth).
# So, like the Admin API resource tests, this runs against a fixture with a
# dummy credential and command = plan: Configure only needs a non-empty
# string, and a resource create's plan never calls the API.
run "federation_issuer_plan_validates_schema" {
  command = plan

  module {
    source = "./tests/fixtures/federation_issuer_plan"
  }

  assert {
    condition     = output.federation_issuer_name == "github-actions"
    error_message = "Expected name to be 'github-actions'."
  }

  assert {
    condition     = output.federation_issuer_issuer_url == "https://token.actions.githubusercontent.com"
    error_message = "Expected issuer_url to be 'https://token.actions.githubusercontent.com'."
  }

  assert {
    condition     = output.federation_issuer_jwks_type == "discovery"
    error_message = "Expected jwks.type to be 'discovery'."
  }

  assert {
    condition     = output.federation_issuer_max_jwt_lifetime_seconds == 600
    error_message = "Expected max_jwt_lifetime_seconds to be 600."
  }
}
