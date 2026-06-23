test {
  parallel = true
}

# command = plan: validate schema without making live API calls.
# anthropic_vault is not yet registered in this worktree, so we
# run plan-only to check the vault_credential schema parses correctly.
run "vault_credential_schema_plan" {
  command = plan
  module { source = "./examples/resources/vault_credential" }

  assert {
    condition     = output.vault_credential_bearer_id != ""
    error_message = "Expected vault_credential_bearer_id to be non-empty."
  }

  assert {
    condition     = output.vault_credential_oauth_id != ""
    error_message = "Expected vault_credential_oauth_id to be non-empty."
  }

  assert {
    condition     = output.vault_credential_env_var_id != ""
    error_message = "Expected vault_credential_env_var_id to be non-empty."
  }
}
