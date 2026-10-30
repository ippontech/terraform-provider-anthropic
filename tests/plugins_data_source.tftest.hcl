test {
  parallel = true
}

# The Plugins API is available to Claude Enterprise organizations only, and the
# test organization is a Claude Console one, so the provider is mocked and the
# public example runs end to end (plan + apply) with no credentials. The mocked
# list is always empty (override_data cannot inject list(object) elements):
# this validates the schema and the example's expressions; pagination, filters
# and mapping are covered by the httptest unit tests.
mock_provider "anthropic" {}

run "plugins_data_source_plan" {
  command = plan

  module {
    source = "../examples/data-sources/plugins"
  }
}

run "plugins_data_source_apply" {
  module {
    source = "../examples/data-sources/plugins"
  }

  assert {
    condition     = output.plugins_to_review != null
    error_message = "Expected the plugins_to_review output to be wired to the data source."
  }
}
