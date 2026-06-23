test {
  parallel = true
}

run "vault_resource_creates_vault" {
  module {
    source = "./examples/resources/vault"
  }

  assert {
    condition     = output.vault_id != ""
    error_message = "Expected vault_id to be non-empty."
  }

  assert {
    condition     = output.vault_created_at != ""
    error_message = "Expected vault_created_at to be non-empty."
  }

  assert {
    condition     = anthropic_vault.minimal.type == "vault"
    error_message = "Expected type == \"vault\"."
  }
}
