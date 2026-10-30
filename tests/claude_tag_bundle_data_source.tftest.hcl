test {
  parallel = true
}

# The Claude Tag API is available to Claude Enterprise organizations only, and
# the test organization is a Claude Console one, so the provider is mocked and
# the public example runs end to end (plan + apply) with no credentials.
# override_data pins the computed values so the assertions prove the example
# wiring; provider logic is covered by the fake-API tests.
mock_provider "anthropic" {
  override_data {
    target = data.anthropic_claude_tag_bundle.support_tools
    values = {
      display_name = "Support tools"
    }
  }
}

run "claude_tag_bundle_data_source_plan" {
  command = plan

  module {
    source = "../examples/data-sources/claude_tag_bundle"
  }
}

run "claude_tag_bundle_data_source_apply" {
  module {
    source = "../examples/data-sources/claude_tag_bundle"
  }

  assert {
    condition     = output.bundle_name == "Support tools"
    error_message = "Expected the mocked name to reach the output."
  }
}
