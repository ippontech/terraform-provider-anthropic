test {
  parallel = true
}

# The RBAC endpoints are Claude Enterprise only and the test organization is a
# Console organization, so the provider is mocked and the public example runs
# end to end (plan + apply) with no credentials. Provider logic (pagination,
# mapping) is covered by the httptest unit tests; this validates schema and
# example wiring. The mocked list is always empty: override_data cannot inject
# list(object) elements.
mock_provider "anthropic" {}

run "rbac_groups_data_source_plan" {
  command = plan

  module {
    source = "../examples/data-sources/rbac_groups"
  }
}

run "rbac_groups_data_source_apply" {
  module {
    source = "../examples/data-sources/rbac_groups"
  }

  assert {
    condition     = output.rbac_group_ids_by_name != null && output.scim_rbac_group_ids != null
    error_message = "Expected both outputs to be computed from the data source."
  }
}
