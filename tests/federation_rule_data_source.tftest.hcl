test {
  parallel = true
}

# OAuth-only data source: CI has no org:admin token (#137), so the provider is
# mocked and the public example runs end to end (plan + apply) with no
# credentials. override_data pins the computed values so the assertions prove
# the example wiring; provider logic is covered by the httptest unit tests (#233).
mock_provider "anthropic" {
  override_data {
    target = data.anthropic_federation_rule.example
    values = {
      name = "gha-deploy"
      target = {
        service_account_id   = "svac_01ABCDEFabcdef0123456789XY"
        service_account_name = "gha-deploy"
      }
    }
  }
}

run "federation_rule_data_source_plan" {
  command = plan

  module {
    source = "../examples/data-sources/federation_rule"
  }
}

run "federation_rule_data_source_apply" {
  module {
    source = "../examples/data-sources/federation_rule"
  }

  assert {
    condition     = output.federation_rule_id == "fdrl_01ABCDEFabcdef0123456789XY"
    error_message = "Expected the literal id to round-trip through the data source."
  }

  assert {
    condition     = output.federation_rule_target_service_account_id == "svac_01ABCDEFabcdef0123456789XY"
    error_message = "Expected the mocked nested target to reach the output, got: ${jsonencode(output.federation_rule_target_service_account_id)}"
  }
}
