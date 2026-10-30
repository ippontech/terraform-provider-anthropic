test {
  parallel = true
}

# The Plugins API is available to Claude Enterprise organizations only, and the
# test organization is a Claude Console one, so the provider is mocked and the
# public example runs end to end (plan + apply) with no credentials.
# override_data pins the computed values so the assertions prove the example
# wiring; provider logic is covered by the httptest unit tests.
mock_provider "anthropic" {
  override_data {
    target = data.anthropic_plugin_marketplace.example
    values = {
      name                            = "engineering-tools"
      default_installation_preference = "available"
    }
  }
}

run "plugin_marketplace_data_source_plan" {
  command = plan

  module {
    source = "../examples/data-sources/plugin_marketplace"
  }
}

run "plugin_marketplace_data_source_apply" {
  module {
    source = "../examples/data-sources/plugin_marketplace"
  }

  assert {
    condition     = output.marketplace_name == "engineering-tools"
    error_message = "Expected the mocked name to reach the output."
  }

  assert {
    condition     = output.marketplace_default_installation_preference == "available"
    error_message = "Expected the mocked default installation setting to reach the output."
  }
}
