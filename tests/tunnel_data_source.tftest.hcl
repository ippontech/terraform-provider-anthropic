test {
  parallel = true
}

# MCP tunnels only accept a workspace:manage_tunnels OAuth token, which CI
# does not have, so the provider is mocked: this validates schema and example
# wiring with no credentials. Read wiring and mapping are covered by the
# httptest unit tests.
mock_provider "anthropic" {}

run "tunnel_data_source_apply" {
  module {
    source = "../examples/data-sources/tunnel"
  }

  assert {
    condition     = data.anthropic_tunnel.example.id == "tnl_01AAAAAAAAAAAAAAAAAAAAAA"
    error_message = "Expected id to echo the configured value."
  }
}
