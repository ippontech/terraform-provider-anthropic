test {
  parallel = true
}

run "vault_resource_plan" {
  command = plan

  module {
    source = "./examples/resources/vault"
  }

  assert {
    condition     = anthropic_vault.minimal.id != ""
    error_message = "vault id should not be empty"
  }

  assert {
    condition     = anthropic_vault.minimal.type == "vault"
    error_message = "vault type should be 'vault'"
  }
}
