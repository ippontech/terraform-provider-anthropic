test {
  parallel = true
}

# Same constraints as tests/tunnel.tftest.hcl (workspace:manage_tunnels token
# only, archive-only objects): plan the public example with a dummy token.
# The plan still runs the ca_certificate_pem validator against the example's
# ca.crt.
provider "anthropic" {
  auth_token = "dummy-auth-token"
}

run "tunnel_certificate_plan" {
  command = plan

  module {
    source = "../examples/resources/tunnel_certificate"
  }

  assert {
    condition     = startswith(anthropic_tunnel_certificate.ca.ca_certificate_pem, "-----BEGIN CERTIFICATE-----")
    error_message = "Expected ca_certificate_pem to hold the example's PEM-encoded CA certificate."
  }
}
