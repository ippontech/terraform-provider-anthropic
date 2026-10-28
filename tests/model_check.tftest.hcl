# Offline tests for the check block of the anthropic_model lifecycle_check
# example: it passes on an active model and fails on a deprecated one. The
# provider is mocked and override_data pins the model, so no credentials are
# needed. The check lives in its own module because a failed check fails a
# terraform test run: in the module that tests/model.tftest.hcl runs against
# the live API, it would turn that run red once the model is deprecated.

mock_provider "anthropic" {}

run "model_check_passes_on_active_model" {
  command = plan

  module {
    source = "../examples/data-sources/model/lifecycle_check"
  }

  override_data {
    target = data.anthropic_model.example
    values = {
      id              = "claude-sonnet-4-5-20250929"
      lifecycle_stage = "active"
    }
  }
}

run "model_check_fails_on_deprecated_model" {
  command = plan

  module {
    source = "../examples/data-sources/model/lifecycle_check"
  }

  override_data {
    target = data.anthropic_model.example
    values = {
      id              = "claude-sonnet-4-5-20250929"
      lifecycle_stage = "deprecated"
      retires_at      = "2027-02-01T00:00:00Z"
    }
  }

  expect_failures = [
    check.model_is_active,
  ]
}
