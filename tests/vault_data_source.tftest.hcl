test {
  parallel = true
}

# Applies for real (apply is the default command): vaults are free to create
# and destroy, and a read straight after a create is consistent on the vaults
# API (only updates and deletes show a staleness window, see CLAUDE.md).
run "vault_data_source_reads_vault" {
  module {
    source = "../examples/data-sources/vault"
  }

  assert {
    condition     = data.anthropic_vault.example.id == anthropic_vault.example.id
    error_message = "Expected the data source id to match the resource id."
  }

  assert {
    condition     = output.vault_display_name == "example-vault"
    error_message = "Expected display_name to be \"example-vault\"."
  }

  assert {
    condition     = output.vault_metadata["team"] == "platform"
    error_message = "Expected metadata.team to be \"platform\"."
  }

  assert {
    condition     = output.vault_archived == false
    error_message = "Expected a freshly created vault not to be archived."
  }
}
