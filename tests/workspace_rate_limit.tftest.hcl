test {
  parallel = true
}

# Admin API resource: no dedicated test organization exists yet (#58), so this
# test plans the public example with a dummy admin key. Configure only needs a
# non-empty credential and a create plan never calls the API (#233).
provider "anthropic" {
  admin_api_key = "dummy-admin-api-key"
}

run "workspace_rate_limit_plan_validates_schema" {
  command = plan

  module {
    source = "../examples/resources/workspace_rate_limit"
  }

  variables {
    sonnet_rate_limit_id = "rl_01ABCDEFGHIJKLMNOPQRSTUV"
  }

  assert {
    condition     = anthropic_workspace_rate_limit.sonnet.rate_limit_id == "rl_01ABCDEFGHIJKLMNOPQRSTUV"
    error_message = "Expected rate_limit_id to come from the variable."
  }

  assert {
    condition     = anthropic_workspace_rate_limit.sonnet.limits["requests_per_minute"] == 100
    error_message = "Expected limits.requests_per_minute to be 100."
  }

  assert {
    condition     = length(anthropic_workspace_rate_limit.sonnet.limits) == 2
    error_message = "Expected exactly the two limiter types of the example to be managed."
  }
}
