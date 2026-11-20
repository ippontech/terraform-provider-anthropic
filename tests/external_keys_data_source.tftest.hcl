test {
  parallel = true
}

# CMEK must be turned on per organization and the terraform-tests organization
# was never checked for it, so the provider is mocked and the public example
# runs end to end (plan + apply) with no credentials. Provider logic
# (page/next_page pagination, mapping) is covered by the httptest unit tests;
# this validates schema and example wiring. The mocked list is always empty:
# override_data cannot inject list(object) elements.
mock_provider "anthropic" {}

run "external_keys_data_source_plan" {
  command = plan

  module {
    source = "../examples/data-sources/external_keys"
  }
}

run "external_keys_data_source_apply" {
  module {
    source = "../examples/data-sources/external_keys"
  }

  assert {
    condition     = output.external_key_ids != null
    error_message = "Expected the external_key_ids output to be wired to the data source."
  }

  assert {
    condition     = output.unattached_external_key_ids != null
    error_message = "Expected the unattached_external_key_ids output to be wired to the data source."
  }
}
