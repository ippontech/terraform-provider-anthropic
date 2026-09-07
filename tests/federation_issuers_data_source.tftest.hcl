test {
  parallel = true
}

# OAuth-only data source: CI has no org:admin token (#137), so the provider is
# mocked and the public example is exercised end to end (plan + apply) with no
# credentials at all. Provider logic (pagination, include_archived query param,
# mapping) is covered by the httptest unit tests; this validates schema and
# example wiring. Note the mocked lists are always empty: override_data cannot
# inject list(object) elements (#233).
mock_provider "anthropic" {}

run "federation_issuers_data_source_plan" {
  command = plan

  module {
    source = "../examples/data-sources/federation_issuers"
  }
}

run "federation_issuers_data_source_apply" {
  module {
    source = "../examples/data-sources/federation_issuers"
  }

  assert {
    condition     = output.federation_issuer_ids != null
    error_message = "Expected federation_issuer_ids to be wired to the data source."
  }

  assert {
    condition     = output.federation_issuers_count >= 0 && output.federation_issuers_count_including_archived >= 0
    error_message = "Expected both count outputs to be computed from the data sources."
  }
}
