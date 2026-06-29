test {
  parallel = true
}

# command = apply: credentials are stored as provided and are not validated until
# session runtime, so applying with placeholder secret material and unreachable
# MCP server URLs creates the records without error and exercises the full
# create/read path across all three auth types. The run is torn down afterwards.
run "vault_credential_apply" {
  command = apply
  module { source = "./examples/resources/vault_credential" }

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
}
