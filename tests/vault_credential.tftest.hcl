test {
  parallel = true
}

# command = plan: applying would create real credentials from fabricated secret
# material and requires reachable MCP servers, so we validate the schema and the
# per-type config validators at plan time only. The credential ids and the
# chained vault id are unknown at plan, so we assert on known input attributes
# rather than computed ids.
run "vault_credential_schema_plan" {
  command = plan
  module { source = "./examples/resources/vault_credential" }

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
}
