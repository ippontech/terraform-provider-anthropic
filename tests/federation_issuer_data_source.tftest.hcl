test {
  parallel = true
}

# OAuth-only data source: CI has no org:admin token (#137), so the provider is
# mocked and the public example runs end to end (plan + apply) with no
# credentials. override_data pins the computed values so the assertions prove
# the example wiring; provider logic is covered by the httptest unit tests (#233).
mock_provider "anthropic" {
  override_data {
    target = data.anthropic_federation_issuer.example
    values = {
      name       = "github-actions"
      issuer_url = "https://token.actions.githubusercontent.com"
    }
  }
}

run "federation_issuer_data_source_plan" {
  command = plan

  module {
    source = "../examples/data-sources/federation_issuer"
  }
}

run "federation_issuer_data_source_apply" {
  module {
    source = "../examples/data-sources/federation_issuer"
  }

  assert {
    condition     = output.federation_issuer_name == "github-actions"
    error_message = "Expected the mocked name to reach the output."
  }

  assert {
    condition     = output.federation_issuer_issuer_url == "https://token.actions.githubusercontent.com"
    error_message = "Expected the mocked issuer_url to reach the output."
  }
}
