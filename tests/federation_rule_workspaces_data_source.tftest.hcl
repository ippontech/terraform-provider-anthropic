test {
  parallel = true
}

# OAuth-only data source: CI has no org:admin token (#137), so the provider is
# mocked and the public example runs end to end (plan + apply) with no
# credentials. Provider logic (pagination, mapping) is covered by the httptest
# unit tests; this validates schema and example wiring. The mocked list is
# always empty: override_data cannot inject list(object) elements (#233).
mock_provider "anthropic" {}

run "federation_rule_workspaces_data_source_plan" {
  command = plan

  module {
    source = "../examples/data-sources/federation_rule_workspaces"
  }
}

run "federation_rule_workspaces_data_source_apply" {
  module {
    source = "../examples/data-sources/federation_rule_workspaces"
  }

  assert {
    condition     = output.federation_rule_workspaces_count >= 0 && output.federation_rule_workspace_ids != null
    error_message = "Expected both outputs to be computed from the data source."
  }
}
