test {
  parallel = true
}

# Federation endpoints require an org:admin OAuth bearer token and reject API
# keys outright (see internal/errors/auth_token.go), and no test org exists
# yet for org-level writes (the same blocker as #58). This runs against a
# fixture with a dummy auth_token and asserts command = plan, so it validates
# the schema without ever making a live API call or needing a real token.
run "federation_rule_plan_validates_schema" {
  command = plan

  module {
    source = "./tests/fixtures/federation_rule_plan"
  }

  assert {
    condition     = output.federation_rule_name == "plan-test-rule"
    error_message = "Expected name to be 'plan-test-rule'."
  }

  assert {
    condition     = output.federation_rule_oauth_scope == "workspace:developer"
    error_message = "Expected oauth_scope to be 'workspace:developer'."
  }

  assert {
    condition     = output.federation_rule_token_lifetime_seconds == 1800
    error_message = "Expected token_lifetime_seconds to be 1800."
  }
}
