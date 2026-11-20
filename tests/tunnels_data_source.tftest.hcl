test {
  parallel = true
}

# MCP tunnels only accept a workspace:manage_tunnels OAuth token, which CI
# does not have, so the provider is mocked and the public example runs end to
# end (plan + apply) with no credentials. Pagination and the include_archived
# query parameter are covered by the httptest unit tests. The mocked list is
# always empty: override_data cannot inject list(object) elements.
mock_provider "anthropic" {}

run "tunnels_data_source_apply" {
  module {
    source = "../examples/data-sources/tunnels"
  }

  assert {
    condition     = data.anthropic_tunnels.all.include_archived == true
    error_message = "Expected include_archived to echo the configured value."
  }

  assert {
    condition     = output.archived_tunnel_count == 0
    error_message = "Expected the archived_tunnel_count output to be wired to the (mocked, empty) list."
  }
}
