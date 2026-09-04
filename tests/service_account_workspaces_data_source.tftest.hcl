test {
  parallel = true
}

# The service account workspaces endpoint requires an org:admin OAuth bearer
# token and rejects API keys outright (see internal/errors/auth_token.go), and
# no test org exists yet for org-level writes (the same blocker as #58). This
# runs against a fixture with a dummy auth_token and asserts command = plan,
# so it validates the schema without ever making a live API call or needing a
# real token.
#
# The fixture defers the data source's read past plan with a `depends_on` on
# a resource that is never applied under `command = plan` (see
# tests/fixtures/service_account_workspaces_data_source_plan/main.tf) — the
# data source's own input (service_account_id) is otherwise fully known, which
# would make Terraform read it live during plan.
run "service_account_workspaces_plan_validates_schema" {
  command = plan

  module {
    source = "./tests/fixtures/service_account_workspaces_data_source_plan"
  }

  assert {
    condition     = output.service_account_workspaces_service_account_id == "svac_01PLANTEST"
    error_message = "Expected service_account_id to be 'svac_01PLANTEST'."
  }
}
