test {
  parallel = true
}

# Federation endpoints require an org:admin OAuth bearer token and reject API
# keys outright (see internal/errors/auth_token.go), and no test org exists
# yet for org-level writes (the same blocker as #58). This runs against a
# fixture with a dummy auth_token and asserts command = plan, so it validates
# the schema without ever making a live API call or needing a real token.
run "federation_rule_workspace_plan_validates_schema" {
  command = plan

  module {
    source = "./tests/fixtures/federation_rule_workspace_plan"
  }

  assert {
    condition     = output.federation_rule_workspace_federation_rule_id == "fdrl_01PLANTEST"
    error_message = "Expected federation_rule_id to be 'fdrl_01PLANTEST'."
  }

  assert {
    condition     = output.federation_rule_workspace_workspace_id == "wrkspc_01PLANTEST"
    error_message = "Expected workspace_id to be 'wrkspc_01PLANTEST'."
  }
}
