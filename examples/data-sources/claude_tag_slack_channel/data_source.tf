# What applies in a channel, wherever each setting is set: the channel, its
# workspace or the organization's Slack defaults.
data "anthropic_claude_tag_slack_channel" "support" {
  slack = {
    workspace_id = "T01ABCD2EFG"
    channel_id   = "C05HJKL6MNP"
  }
  include_inherited = true
}

output "support_channel_enabled" {
  description = "Whether Claude is on in the channel."
  value       = data.anthropic_claude_tag_slack_channel.support.enabled
}

output "support_channel_model" {
  description = "The model new sessions in the channel start on (null: the product's default)."
  value       = data.anthropic_claude_tag_slack_channel.support.model
}
