test {
  parallel = true
}

# The Plugins API is available to Claude Enterprise organizations only, and the
# test organization is a Claude Console one, so the provider is mocked and the
# public example runs end to end (plan + apply) with no credentials. The mocked
# list is always empty; pagination and mapping are covered by the httptest unit
# tests.
mock_provider "anthropic" {}

run "plugin_versions_data_source_plan" {
  command = plan

  module {
    source = "../examples/data-sources/plugin_versions"
  }
}

run "plugin_versions_data_source_apply" {
  module {
    source = "../examples/data-sources/plugin_versions"
  }

  assert {
    condition     = output.plugin_version_ids != null
    error_message = "Expected the plugin_version_ids output to be wired to the data source."
  }
}
