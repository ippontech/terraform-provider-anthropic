test {
  parallel = true
}

# Self-contained list data source: the provider is mocked and the public
# example runs end to end (plan + apply) with no credentials. Provider logic
# (pagination, include_archived query param, mapping) is covered by the
# httptest unit tests; this validates schema and example wiring. The mocked
# lists are always empty: override_data cannot inject list(object) elements
# (#233).
mock_provider "anthropic" {}

run "vaults_data_source_plan" {
  command = plan

  module {
    source = "../examples/data-sources/vaults"
  }
}

run "vaults_data_source_apply" {
  module {
    source = "../examples/data-sources/vaults"
  }

  assert {
    condition     = output.vault_ids != null
    error_message = "Expected vault_ids to be wired to the data source."
  }

  assert {
    condition     = output.archived_vault_count >= 0
    error_message = "Expected archived_vault_count to be computed from the data source."
  }

  assert {
    condition     = data.anthropic_vaults.all.include_archived == true
    error_message = "Expected include_archived to echo the configured value."
  }
}
