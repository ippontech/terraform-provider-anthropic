test {
  parallel = true
}

# The Claude Tag API is available to Claude Enterprise organizations only, and
# the test organization is a Claude Console one, so the provider is mocked and
# the public example runs end to end (plan + apply) with no credentials. The
# mocked list is always empty (override_data cannot inject list(object)
# elements): this validates the schema and the example's expressions.
mock_provider "anthropic" {}

run "claude_tag_slack_workspaces_data_source_plan" {
  command = plan

  module {
    source = "../examples/data-sources/claude_tag_slack_workspaces"
  }
}

run "claude_tag_slack_workspaces_data_source_apply" {
  module {
    source = "../examples/data-sources/claude_tag_slack_workspaces"
  }

  assert {
    condition     = length(output.workspaces_open_to_guests) == 0
    error_message = "Expected the audit output to be wired to the (empty, mocked) list."
  }
}
