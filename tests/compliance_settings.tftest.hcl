test {
  parallel = true
}

# Admin API resource: no dedicated test organization exists yet (#58), and
# toggling the Compliance API on a shared organization starts or stops its
# activity recording, so this test only plans the public example with a dummy
# admin key. Configure only needs a non-empty credential and a create plan
# never calls the API (#233).
provider "anthropic" {
  admin_api_key = "dummy-admin-api-key"
}

run "compliance_settings_plan_validates_schema" {
  command = plan

  module {
    source = "../examples/resources/compliance_settings"
  }

  assert {
    condition     = anthropic_compliance_settings.this.state == "enabled"
    error_message = "Expected the planned Compliance API state to be 'enabled'."
  }
}
