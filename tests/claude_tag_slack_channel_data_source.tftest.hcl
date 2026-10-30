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
    target = data.anthropic_claude_tag_slack_channel.support
    values = {
      enabled = true
      model   = "claude-opus-5-5"
    }
  }
}

run "claude_tag_slack_channel_data_source_plan" {
  command = plan

  module {
    source = "../examples/data-sources/claude_tag_slack_channel"
  }
}

run "claude_tag_slack_channel_data_source_apply" {
  module {
    source = "../examples/data-sources/claude_tag_slack_channel"
  }

  assert {
    condition     = output.support_channel_enabled == true
    error_message = "Expected the mocked enabled value to reach the output."
  }

  assert {
    condition     = output.support_channel_model == "claude-opus-5-5"
    error_message = "Expected the mocked model to reach the output."
  }
}
