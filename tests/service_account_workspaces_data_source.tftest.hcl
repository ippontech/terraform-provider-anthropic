test {
  parallel = true
}

# OAuth-only data source: CI has no org:admin token (#137), so the provider is
# mocked and the public example runs end to end (plan + apply) with no
# credentials. Provider logic (pagination, mapping) is covered by the httptest
# unit tests; this validates schema and example wiring. The mocked list is
# always empty: override_data cannot inject list(object) elements (#233).
mock_provider "anthropic" {}

run "service_account_workspaces_data_source_plan" {
  command = plan

  module {
    source = "../examples/data-sources/service_account_workspaces"
  }
}

run "service_account_workspaces_data_source_apply" {
  module {
    source = "../examples/data-sources/service_account_workspaces"
  }

  assert {
    condition     = output.service_account_workspaces_count >= 0
    error_message = "Expected the count output to be computed from the data source."
  }
}
