test {
  parallel = true
}

# OAuth-only data source: CI has no org:admin token (#137), so the provider is
# mocked and the public example is exercised end to end (plan + apply) with no
# credentials at all. Provider logic (pagination, issuer_id/include_archived
# query params, mapping) is covered by the httptest unit tests; this validates
# schema and example wiring. Note the mocked lists are always empty:
# override_data cannot inject list(object) elements (#233).
mock_provider "anthropic" {}

run "federation_rules_data_source_plan" {
  command = plan

  module {
    source = "../examples/data-sources/federation_rules"
  }
}

run "federation_rules_data_source_apply" {
  module {
    source = "../examples/data-sources/federation_rules"
  }

  assert {
    condition     = output.federation_rules_count >= 0 && output.federation_rule_names != null
    error_message = "Expected the list outputs to be wired to the data source."
  }

  assert {
    condition     = output.rules_for_issuer != null
    error_message = "Expected the issuer-filtered output to be wired to the data source."
  }
}
