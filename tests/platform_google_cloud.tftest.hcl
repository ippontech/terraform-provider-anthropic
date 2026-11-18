test {
  parallel = true
}

# Claude Models on Google Cloud (Anthropic managed), planned offline: there is
# no Google Cloud test organization, so these runs only plan public examples
# through a provider alias configured with the google_cloud block. A static
# access token keeps Configure from looking up Application Default
# Credentials, and nothing is sent under command = plan; the base_url is a
# closed local port so that an accidental request fails here instead of
# reaching the gateway.
provider "anthropic" {
  workspace_id = "wrkspc_01PlanOnlyWorkspace"

  google_cloud = {
    project      = "plan-only-project"
    access_token = "ya29.plan-only-token"
    base_url     = "http://127.0.0.1:9"
  }
}

# A resource the gateway serves plans as it does first party.
run "agent_plans_on_google_cloud" {
  command = plan

  module {
    source = "../examples/resources/agent"
  }

  assert {
    condition     = anthropic_agent.simple.name == "Simple Agent"
    error_message = "Expected the agent to plan on Claude Models on Google Cloud."
  }
}

# A resource the gateway does not serve (anthropic_workspace, for instance)
# fails at plan time with a diagnostic naming the platform. That case is tested
# in Go (TestGoogleCloudGateThroughTheProtocol): expect_failures only matches
# custom condition failures, never a provider's error diagnostic.
