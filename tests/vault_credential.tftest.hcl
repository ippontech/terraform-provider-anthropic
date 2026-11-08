test {
  parallel = true
}

# Applies for real (apply is the default command): credentials are stored as
# provided and are not validated until session runtime, so placeholder secret
# material and unreachable MCP server URLs create the records without error and
# exercise the full create/read path across all three auth types. Torn down
# afterwards.
run "vault_credential_apply" {
  module { source = "../examples/resources/vault_credential" }

  assert {
    condition     = startswith(anthropic_vault_credential.bearer.id, "vcrd_")
    error_message = "Expected bearer credential id to start with \"vcrd_\"."
  }

  assert {
    condition     = anthropic_vault_credential.bearer.type == "static_bearer"
    error_message = "Expected bearer credential type == \"static_bearer\"."
  }

  assert {
    condition     = anthropic_vault_credential.oauth.type == "mcp_oauth"
    error_message = "Expected oauth credential type == \"mcp_oauth\"."
  }

  assert {
    condition     = anthropic_vault_credential.env_var.type == "environment_variable"
    error_message = "Expected env_var credential type == \"environment_variable\"."
  }

  assert {
    condition     = anthropic_vault_credential.env_var.networking.mode == "limited"
    error_message = "Expected env_var networking mode == \"limited\"."
  }

  assert {
    condition     = anthropic_vault_credential.env_var.credential_type == "vault_credential"
    error_message = "Expected env_var credential_type == \"vault_credential\"."
  }

  assert {
    condition     = anthropic_vault_credential.env_var.injection_location.header && !anthropic_vault_credential.env_var.injection_location.body
    error_message = "Expected env_var to substitute its secret in headers only."
  }

  assert {
    condition     = anthropic_vault_credential.bearer.injection_location == null
    error_message = "Expected no injection_location on a static_bearer credential."
  }
}
