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
    target = data.anthropic_plugin.example
    values = {
      name  = "sales-toolkit"
      reach = "remote"
    }
  }
}

run "plugin_data_source_plan" {
  command = plan

  module {
    source = "../examples/data-sources/plugin"
  }
}

run "plugin_data_source_apply" {
  module {
    source = "../examples/data-sources/plugin"
  }

  assert {
    condition     = output.plugin_name == "sales-toolkit"
    error_message = "Expected the mocked name to reach the output."
  }

  assert {
    condition     = output.plugin_reach == "remote"
    error_message = "Expected the mocked reach to reach the output."
  }
}
