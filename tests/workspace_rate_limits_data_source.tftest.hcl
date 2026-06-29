test {
  parallel = true
}

# The dedicated "terraform-tests" workspace (Go tests use acctest.TerraformTestsWorkspaceID).
variables {
  workspace_id = "wrkspc_01HMrPGQfWoZ5LnhFhxuvNsm"
}

run "workspace_rate_limits_data_source_lists_overrides" {
  parallel = true

  module {
    source = "./examples/data-sources/workspace_rate_limits"
  }

  variables {
    workspace_id = var.workspace_id
  }

  assert {
    condition     = output.rate_limits_count >= 0
    error_message = "Expected rate_limits_count to be a non-negative number."
  }
}
