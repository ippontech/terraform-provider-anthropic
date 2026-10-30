test {
  parallel = true
}

# The Claude Tag API is available to Claude Enterprise organizations only, and
# the test organization is a Claude Console one, so the provider is mocked and
# the public example runs end to end (plan + apply) with no credentials. The
# mocked list is always empty (override_data cannot inject list(object)
# elements): this validates the schema and the example's expressions;
# pagination and mapping are covered by the fake-API tests.
mock_provider "anthropic" {}

run "claude_tag_bundles_data_source_plan" {
  command = plan

  module {
    source = "../examples/data-sources/claude_tag_bundles"
  }
}

run "claude_tag_bundles_data_source_apply" {
  module {
    source = "../examples/data-sources/claude_tag_bundles"
  }

  assert {
    condition     = output.bundle_ids_by_name != null
    error_message = "Expected the bundle_ids_by_name output to be wired to the data source."
  }
}
