test {
  parallel = true
}

# Service account workspace endpoints require an org:admin OAuth bearer token
# and reject API keys outright (see internal/errors/auth_token.go), and no
# test org exists yet for org-level writes (the same blocker as #58). This
# runs against a fixture with a dummy auth_token and asserts command = plan,
# so it validates the schema without ever making a live API call or needing a
# real token.
run "service_account_workspace_plan_validates_schema" {
  command = plan

  module {
    source = "./tests/fixtures/service_account_workspace_plan"
  }

  assert {
    condition     = output.service_account_workspace_service_account_id == "svac_01PLANTEST"
    error_message = "Expected service_account_id to be 'svac_01PLANTEST'."
  }

  assert {
    condition     = output.service_account_workspace_workspace_id == "wrkspc_01PLANTEST"
    error_message = "Expected workspace_id to be 'wrkspc_01PLANTEST'."
  }

  assert {
    condition     = output.service_account_workspace_workspace_role == "workspace_developer"
    error_message = "Expected workspace_role to be 'workspace_developer'."
  }
}
