test {
  parallel = true
}

# The user_profiles beta is not enabled for the terraform-tests organization
# (verified 2026-09-30: GET /v1/user_profiles returns 404 for the standard
# key), so the provider is mocked and the public example is exercised end to
# end (plan + apply) with no credentials at all. Provider logic (pagination,
# order query param, mapping) is covered by the httptest unit tests; this
# validates schema and example wiring. Note the mocked list is always empty:
# override_data cannot inject list(object) elements.
mock_provider "anthropic" {}

run "user_profiles_data_source_plan" {
  command = plan

  module {
    source = "../examples/data-sources/user_profiles"
  }
}

run "user_profiles_data_source_apply" {
  module {
    source = "../examples/data-sources/user_profiles"
  }

  assert {
    condition     = output.user_profiles != null
    error_message = "Expected the user_profiles output to be wired to the data source."
  }
}
