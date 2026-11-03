test {
  parallel = true
}

# MCP tunnels only accept an OAuth bearer token with the
# workspace:manage_tunnels scope, which CI does not have, and a tunnel can
# only be archived, never deleted. So this test plans the public example with
# a dummy token: Configure only needs a non-empty credential and a create plan
# never calls the API (#233).
provider "anthropic" {
  auth_token = "dummy-auth-token"
}

run "tunnel_plan" {
  command = plan

  module {
    source = "../examples/resources/tunnel"
  }

  assert {
    condition     = anthropic_tunnel.corp.display_name == "corp-mcp"
    error_message = "Expected display_name to be 'corp-mcp'."
  }

  assert {
    condition     = anthropic_tunnel.corp.token_rotation == "1"
    error_message = "Expected token_rotation to be '1'."
  }
}
