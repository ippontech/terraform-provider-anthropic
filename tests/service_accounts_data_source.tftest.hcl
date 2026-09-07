test {
  parallel = true
}

# OAuth-only data source: CI has no org:admin token (#137), so the provider is
# mocked and the public example is exercised end to end (plan + apply) with no
# credentials at all. Provider logic (pagination, mapping) is covered by the
# httptest unit tests; this validates schema and example wiring. Note the mocked
# list is always empty: override_data cannot inject list(object) elements (#233).
mock_provider "anthropic" {}

run "service_accounts_data_source_plan" {
  command = plan

  module {
    source = "../examples/data-sources/service_accounts"
  }
}

run "service_accounts_data_source_apply" {
  module {
    source = "../examples/data-sources/service_accounts"
  }

  assert {
    condition     = output.service_accounts != null
    error_message = "Expected the service_accounts output to be wired to the data source."
  }
}
