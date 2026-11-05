test {
  parallel = true
}

# Admin API resource: setting spend limits needs a Claude Enterprise
# organization (or the Console early access preview) and there is no dedicated
# test organization (#58), so this test only plans the public example against
# a dummy admin credential. Configure only needs a non-empty key and a create
# plan never calls the API.
provider "anthropic" {
  admin_api_key = "dummy-admin-api-key"
}

run "spend_limit_plan" {
  command = plan

  module {
    source = "../examples/resources/spend_limit"
  }

  assert {
    condition     = anthropic_spend_limit.member.scope.type == "user" && anthropic_spend_limit.member.amount == "50000"
    error_message = "Expected a user limit of 50000."
  }

  assert {
    condition     = anthropic_spend_limit.member_daily_unlimited.period == "daily" && anthropic_spend_limit.member_daily_unlimited.amount == null
    error_message = "Expected a daily no-limit override (null amount)."
  }
}
