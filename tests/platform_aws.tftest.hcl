test {
  parallel = true
}

# Claude Platform on AWS, planned offline: there is no AWS test organization,
# so these runs only plan public examples through a provider alias configured
# with the aws block. Static example credentials keep Configure from reading
# any AWS credential chain, and nothing is sent under command = plan; the
# base_url is a closed local port so that an accidental request fails here
# instead of reaching the gateway.
provider "anthropic" {
  workspace_id = "wrkspc_01PlanOnlyWorkspace"

  aws = {
    region     = "us-west-2"
    access_key = "AKIAIOSFODNN7EXAMPLE"
    secret_key = "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY"
    base_url   = "http://127.0.0.1:9"
  }
}

# A resource the gateway serves plans as it does first party.
run "agent_plans_on_aws" {
  command = plan

  module {
    source = "../examples/resources/agent"
  }

  assert {
    condition     = anthropic_agent.simple.name == "Simple Agent"
    error_message = "Expected the agent to plan on Claude Platform on AWS."
  }
}

# A resource the gateway does not serve (anthropic_invite, for instance) fails
# at plan time with a diagnostic naming the platform. That case is tested in
# Go (TestAWSGateThroughTheProtocol): expect_failures only matches custom
# condition failures, never a provider's error diagnostic.
