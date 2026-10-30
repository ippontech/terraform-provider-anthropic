test {
  parallel = true
}

# The Claude Tag API is available to Claude Enterprise organizations only, and
# the test organization is a Claude Console one, so this test only plans the
# public example against a dummy admin credential.
provider "anthropic" {
  admin_api_key = "dummy-admin-api-key"
}

run "claude_tag_slack_workspace_settings_plan" {
  command = plan

  module {
    source = "../examples/resources/claude_tag_slack_workspace_settings"
  }

  assert {
    condition     = anthropic_claude_tag_slack_workspace_settings.main.slack.workspace_id == "T01ABCD2EFG"
    error_message = "Expected the workspace to be found by Slack's own workspace ID."
  }

  assert {
    condition     = anthropic_claude_tag_slack_workspace_settings.main.enabled == null
    error_message = "Expected enabled to stay inherited from the Slack defaults."
  }
}
