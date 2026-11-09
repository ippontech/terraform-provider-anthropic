test {
  parallel = true
}

# Claude Enterprise Admin API resource: the test organization is a Console
# organization and there is no dedicated test organization (#58), so this test
# only plans the public example against a dummy admin credential. Configure
# only needs a non-empty key and a create plan never calls the API.
provider "anthropic" {
  admin_api_key = "dummy-admin-api-key"
}

run "rbac_group_plan" {
  command = plan

  module {
    source = "../examples/resources/rbac_group"
  }

  assert {
    condition     = anthropic_rbac_group.example.name == "Engineering"
    error_message = "Expected the group name to be 'Engineering'."
  }
}
