test {
  parallel = true
}

# The RBAC endpoints are Claude Enterprise only and the test organization is a
# Console organization, so the provider is mocked and the public example runs
# end to end (plan + apply) with no credentials. override_data pins the
# computed values so the assertions prove the example wiring; provider logic
# is covered by the httptest unit tests.
mock_provider "anthropic" {
  override_data {
    target = data.anthropic_rbac_group.example
    values = {
      name        = "Engineering"
      source_type = "direct"
      role_ids    = ["rbac_role_01CdEfGhIjKlMnOpQrStUv"]
    }
  }
}

run "rbac_group_data_source_plan" {
  command = plan

  module {
    source = "../examples/data-sources/rbac_group"
  }
}

run "rbac_group_data_source_apply" {
  module {
    source = "../examples/data-sources/rbac_group"
  }

  assert {
    condition     = output.rbac_group_name == "Engineering"
    error_message = "Expected the mocked name to reach the output."
  }

  assert {
    condition     = output.rbac_group_role_ids == toset(["rbac_role_01CdEfGhIjKlMnOpQrStUv"])
    error_message = "Expected the mocked role IDs to reach the output."
  }
}
