test {
  parallel = true
}

# OAuth-only data source: CI has no org:admin token (#137), so the provider is
# mocked and the public example runs end to end (plan + apply) with no
# credentials. override_data pins the computed values so the assertions prove
# the example wiring; provider logic is covered by the httptest unit tests (#233).
mock_provider "anthropic" {
  override_data {
    target = data.anthropic_service_account.example
    values = {
      name              = "ci-runner"
      organization_role = "developer"
    }
  }
}

run "service_account_data_source_plan" {
  command = plan

  module {
    source = "../examples/data-sources/service_account"
  }
}

run "service_account_data_source_apply" {
  module {
    source = "../examples/data-sources/service_account"
  }

  assert {
    condition     = output.service_account_id == "svac_01WCz1FkmYMm4gnmykNKUu3Q"
    error_message = "Expected the literal id to round-trip through the data source."
  }

  assert {
    condition     = output.service_account_organization_role == "developer"
    error_message = "Expected the mocked organization_role to reach the output."
  }
}
