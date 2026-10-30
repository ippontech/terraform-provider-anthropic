resource "anthropic_claude_tag_slack_channel" "support" {
  slack = {
    workspace_id = "T01ABCD2EFG"
    channel_id   = "C05HJKL6MNP"
  }
  enabled = true
}

# Give the support channel the "Support tools" Access bundle, in the same run
# that sets the channel up, so its first session already has its tools.
resource "anthropic_claude_tag_bundle_target" "support_tools" {
  bundle_id        = "capp_011CZkZjLP1cWxj5EtMmdgbq"
  slack_channel_id = anthropic_claude_tag_slack_channel.support.id
}

# Make the "Monitoring" bundle available in a whole workspace (its slws_ ID,
# for instance anthropic_claude_tag_slack_workspace_settings.main.id).
resource "anthropic_claude_tag_bundle_target" "monitoring" {
  bundle_id          = "capp_011CZkZqWx4mTr8vLp2NbHsK"
  slack_workspace_id = "slws_011CZkZnWa7xPc3uJfZe5LgT"
}

output "support_tools_target_id" {
  description = "ID of the support channel's attachment (btgt_...)."
  value       = anthropic_claude_tag_bundle_target.support_tools.id
}
