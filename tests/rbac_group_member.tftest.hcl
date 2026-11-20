test {
  parallel = true
}

# Claude Enterprise Admin API resource: plan-only against a dummy admin
# credential, like rbac_group.tftest.hcl (Console test organization, #58).
provider "anthropic" {
  admin_api_key = "dummy-admin-api-key"
}

run "rbac_group_member_plan" {
  command = plan

  module {
    source = "../examples/resources/rbac_group_member"
  }

  assert {
    condition     = anthropic_rbac_group_member.example.user_id == "user_01AbCdEfGhIjKlMnOpQrSt"
    error_message = "Expected user_id to be 'user_01AbCdEfGhIjKlMnOpQrSt'."
  }
}
