test {
  parallel = true
}

# The user_profiles beta is not enabled for the terraform-tests organization
# (verified 2026-09-30: GET /v1/user_profiles returns 404 for the standard
# key), so the provider is mocked and the public example runs end to end
# (plan + apply) with no credentials. override_data pins the computed values
# so the assertions prove the example wiring; provider logic is covered by
# the httptest unit tests.
mock_provider "anthropic" {
  override_data {
    target = data.anthropic_user_profile.example
    values = {
      access_type = "application"
      name        = "Jane Doe"
    }
  }
}

run "user_profile_data_source_plan" {
  command = plan

  module {
    source = "../examples/data-sources/user_profile"
  }
}

run "user_profile_data_source_apply" {
  module {
    source = "../examples/data-sources/user_profile"
  }

  assert {
    condition     = output.user_profile_id == "uprof_01AAAAAAAAAAAAAAAAAAAAAA"
    error_message = "Expected the literal id to round-trip through the data source."
  }

  assert {
    condition     = output.user_profile_access_type == "application"
    error_message = "Expected the mocked access_type to reach the output."
  }
}
