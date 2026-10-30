test {
  parallel = true
}

# The Claude Tag API is available to Claude Enterprise organizations only, and
# the test organization is a Claude Console one, so this test only plans the
# public example against a dummy admin credential. The CRUD logic is covered by
# the fake-API tests in internal/services/claudetag.
provider "anthropic" {
  admin_api_key = "dummy-admin-api-key"
}

run "claude_tag_slack_channel_plan" {
  command = plan

  module {
    source = "../examples/resources/claude_tag_slack_channel"
  }

  assert {
    condition     = anthropic_claude_tag_slack_channel.support.slack.channel_id == "C05HJKL6MNP"
    error_message = "Expected the channel to be keyed by Slack's own channel ID."
  }

  assert {
    condition     = anthropic_claude_tag_slack_channel.support.enabled == true
    error_message = "Expected Claude to be turned on in the approved channel."
  }

  assert {
    condition     = anthropic_claude_tag_slack_channel.support.guest_policy == "restrict"
    error_message = "Expected the channel to restrict Claude while a guest is in it."
  }

  assert {
    condition     = anthropic_claude_tag_slack_channel.support.environment_id == null
    error_message = "Expected the environment to stay inherited."
  }
}
