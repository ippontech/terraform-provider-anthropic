test {
  parallel = true
}

# MCP tunnels only accept a workspace:manage_tunnels OAuth token, which CI
# does not have, so the provider is mocked and the public example runs end to
# end (plan + apply) with no credentials. Pagination, mapping and the
# include_archived query parameter are covered by the httptest unit tests. The
# mocked list is always empty: override_data cannot inject list(object)
# elements.
mock_provider "anthropic" {}

run "tunnel_certificates_data_source_apply" {
  module {
    source = "../examples/data-sources/tunnel_certificates"
  }

  assert {
    condition     = data.anthropic_tunnel_certificates.trusted.tunnel_id == "tnl_01AAAAAAAAAAAAAAAAAAAAAA"
    error_message = "Expected tunnel_id to echo the configured value."
  }

  assert {
    condition     = data.anthropic_tunnel_certificates.all.include_archived == true
    error_message = "Expected include_archived to echo the configured value."
  }

  assert {
    condition     = output.archived_certificate_count == 0
    error_message = "Expected the archived_certificate_count output to be wired to the (mocked, empty) list."
  }
}
