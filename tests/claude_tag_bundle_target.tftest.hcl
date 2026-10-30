test {
  parallel = true
}

# The Claude Tag API is available to Claude Enterprise organizations only, and
# the test organization is a Claude Console one, so this test only plans the
# public example against a dummy admin credential.
provider "anthropic" {
  admin_api_key = "dummy-admin-api-key"
}

run "claude_tag_bundle_target_plan" {
  command = plan

  module {
    source = "../examples/resources/claude_tag_bundle_target"
  }

  assert {
    condition     = anthropic_claude_tag_bundle_target.support_tools.bundle_id == "capp_011CZkZjLP1cWxj5EtMmdgbq"
    error_message = "Expected the support tools bundle to be attached."
  }

  assert {
    condition     = anthropic_claude_tag_bundle_target.monitoring.slack_workspace_id == "slws_011CZkZnWa7xPc3uJfZe5LgT"
    error_message = "Expected the monitoring bundle to target the workspace."
  }

  assert {
    condition     = anthropic_claude_tag_bundle_target.monitoring.slack_channel_id == null
    error_message = "Expected the workspace attachment to name no channel."
  }
}
